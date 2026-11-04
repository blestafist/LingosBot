mod common;

use common::{Reply, Server};
use serde_json::{Value, json};
use std::path::PathBuf;
use std::process::Command;

struct TempConfig(PathBuf);

impl TempConfig {
    fn new(base: &str) -> Self {
        let path = std::env::temp_dir().join(format!(
            "lingos-output-test-{}-{}.json",
            std::process::id(),
            fastrand::u64(..)
        ));
        std::fs::write(
            &path,
            json!({
                "baseUrl": base,
                "username": "file-user",
                "password": "private-file-password"
            })
            .to_string(),
        )
        .unwrap();
        Self(path)
    }
}

impl Drop for TempConfig {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

fn command() -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_lingosbot"));
    command
        .env_remove("LINGOS_USERNAME")
        .env_remove("LINGOS_PASSWORD");
    command
}

fn limited_site() -> Server {
    Server::start(|request, _| {
        Some(match (request.method.as_str(), request.path.as_str()) {
            ("GET", "/h/login") => Reply::html("<input name=_csrf_token value=token>"),
            ("POST", "/h/login") => {
                assert!(request.body.contains("_username=file-user"));
                assert!(request.body.contains("_password=private-file-password"));
                Reply::redirect("/student/dashboard")
            }
            ("GET", "/student/dashboard") => Reply::page(json!({
                "page": "dashboard",
                "dashboard": {
                    "currentGroup": {"id": 1}, "groups": [{"id": 1, "name": "A"}],
                    "contexts": {}, "lessonLimitReached": true, "challenges": []
                }
            })),
            _ => panic!("Unexpected request {} {}", request.method, request.path),
        })
    })
}

#[test]
fn parse_errors_distinguish_json_flags_from_passwords_and_trailing_values() {
    for (args, json_output) in [
        (vec!["-p", "--json", "--unknown"], false),
        (vec!["--password", "--json", "--unknown"], false),
        (vec!["--password=--json", "--unknown"], false),
        (vec!["-p--json", "--unknown"], false),
        (vec!["--", "--json"], false),
        (vec!["--password=secret", "--", "--json"], false),
        (vec!["-p--json", "--json", "--unknown"], true),
        (vec!["--password=--json", "--json", "--unknown"], true),
        (vec!["-p", "--", "--json", "--unknown"], true),
        (vec!["--json", "--password", "--json", "--unknown"], true),
        (vec!["--json", "--lessons", "private-invalid-number"], true),
        // Partial matches intentionally stop at the first parse error.
        (vec!["--unknown", "--json"], false),
    ] {
        let output = command().args(&args).output().unwrap();
        assert_eq!(output.status.code(), Some(3), "{args:?}");
        assert!(!String::from_utf8_lossy(&output.stdout).contains("private-invalid-number"));
        assert!(!String::from_utf8_lossy(&output.stderr).contains("private-invalid-number"));
        if json_output {
            let value: Value = serde_json::from_slice(&output.stdout)
                .unwrap_or_else(|_| panic!("Expected JSON for {args:?}: {output:?}"));
            assert_eq!(value["error_code"], "CONFIG_ERROR");
            assert_eq!(value["success"], false);
            assert_eq!(value.as_object().unwrap().len(), 3);
            assert!(output.stderr.is_empty());
        } else {
            assert!(output.stdout.is_empty(), "{args:?}");
            assert!(String::from_utf8_lossy(&output.stderr).contains("CONFIG_ERROR"));
        }
    }
}

#[test]
fn empty_env_preserves_file_credentials_but_explicit_cli_empty_is_an_error() {
    let server = limited_site();
    let config = TempConfig::new(&server.base);
    let output = command()
        .args(["--json", "--config"])
        .arg(&config.0)
        .env("LINGOS_USERNAME", "")
        .env("LINGOS_PASSWORD", "")
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(0), "{output:?}");
    let value: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(value["username"], "file-user");
    assert_eq!(value["success"], true);
    let requests = server.count();
    for flag in ["--username=", "--password="] {
        let output = command()
            .args(["--json", "--config"])
            .arg(&config.0)
            .arg(flag)
            .env("LINGOS_USERNAME", "environment-user")
            .env("LINGOS_PASSWORD", "private-environment-password")
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(3));
        let value: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(value["error_code"], "CONFIG_ERROR");
        assert!(!String::from_utf8_lossy(&output.stdout).contains("private-"));
    }
    assert_eq!(server.count(), requests);
}

#[cfg(target_os = "linux")]
#[test]
fn unwritable_stdout_preserves_original_failures_but_fails_success_with_code_two() {
    let full = || {
        std::fs::OpenOptions::new()
            .write(true)
            .open("/dev/full")
            .unwrap()
    };
    let output = command().arg("--json").stdout(full()).output().unwrap();
    assert_eq!(output.status.code(), Some(3));

    let server = Server::start(|_, index| {
        Some(if index == 1 {
            Reply::html("<input name=_csrf_token value=token>")
        } else {
            Reply::redirect("/h/login")
        })
    });
    let config = TempConfig::new(&server.base);
    let output = command()
        .args(["--json", "--config"])
        .arg(&config.0)
        .stdout(full())
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(1));

    let server = limited_site();
    let config = TempConfig::new(&server.base);
    for json_output in [false, true] {
        let mut cmd = command();
        cmd.arg("--config").arg(&config.0).stdout(full());
        if json_output {
            cmd.arg("--json");
        }
        let output = cmd.output().unwrap();
        assert_eq!(output.status.code(), Some(2));
    }
}
