use super::*;
use clap::error::ErrorKind;
use std::fs;
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};

static NEXT_FILE: AtomicU64 = AtomicU64::new(0);

struct TestFile(PathBuf);

impl TestFile {
    fn new(contents: impl AsRef<[u8]>) -> Self {
        let path = std::env::temp_dir().join(format!(
            "lingos-config-{}-{}.json",
            std::process::id(),
            NEXT_FILE.fetch_add(1, Ordering::Relaxed)
        ));
        let mut file = fs::OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&path)
            .unwrap();
        std::io::Write::write_all(&mut file, contents.as_ref()).unwrap();
        Self(path)
    }

    fn cli(&self) -> Cli {
        Cli {
            config: Some(self.0.clone()),
            ..Cli::default()
        }
    }
}

impl Drop for TestFile {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

fn valid_config() -> Config {
    Config {
        username: "student@example.test".to_owned(),
        password: "test-password".to_owned(),
        ..Config::default()
    }
}

fn resolve_json(value: serde_json::Value) -> Result<Config> {
    TestFile::new(serde_json::to_vec(&value).unwrap())
        .cli()
        .resolve()
}

fn with_setting(name: &str, value: serde_json::Value) -> Result<Config> {
    let mut settings = serde_json::json!({"username": "student", "password": "secret"});
    settings[name] = value;
    resolve_json(settings)
}

#[test]
fn defaults_are_safe_and_require_explicit_credentials() {
    let config = Config::default();
    assert_eq!(config.base_url, "https://lingos.pl");
    assert_eq!(
        config.student_dashboard_url,
        "https://lingos.pl/student/dashboard"
    );
    assert_eq!(config.lesson_count, 1);
    assert!(config.class_lesson_counts.is_empty());
    assert_eq!(config.lesson_prompt_safety_cap, 30);
    assert_eq!(config.errors_per100_words, 0);
    assert_eq!(config.answer_delay_min_ms, 0);
    assert_eq!(config.answer_delay_max_ms, 0);
    assert_eq!(config.request_timeout_seconds, 30);
    assert_eq!(config.max_run_seconds, 900);
    assert!(config.validate().is_err());
    assert!(Cli::default().resolve().is_err());
    assert!(valid_config().validate().is_ok());
}

#[test]
fn cli_overrides_json_and_preserves_password_whitespace() {
    let file = TestFile::new(
        r#"{"username":"file-user","password":"file-password","lessonCount":2,
                "answerDelayMinMs":100,"answerDelayMaxMs":200,
                "requestTimeoutSeconds":10,"maxRunSeconds":100}"#,
    );
    let config = Cli {
        username: Some("  cli-user  ".to_owned()),
        password: Some("  -cli-password  ".to_owned()),
        lessons: Some(3),
        delay_min_ms: Some(300),
        delay_max_ms: Some(400),
        timeout_seconds: Some(20),
        max_run_seconds: Some(200),
        ..file.cli()
    }
    .resolve()
    .unwrap();
    assert_eq!(config.username, "cli-user");
    assert_eq!(config.password, "  -cli-password  ");
    assert_eq!(config.lesson_count, 3);
    assert_eq!(config.answer_delay_min_ms, 300);
    assert_eq!(config.answer_delay_max_ms, 400);
    assert_eq!(config.request_timeout_seconds, 20);
    assert_eq!(config.max_run_seconds, 200);
}

#[test]
fn json_sources_share_the_same_model_and_cli_overrides() {
    let json = r#"{"credentials":{"email":"desktop","password":"desktop-pass"},
        "username":"file-user","password":"file-pass","mode":"api",
        "lessonCount":2,"classLessonCounts":{"group:42":3,"disabled":0},
        "answerDelayMinMs":100,"answerDelayMaxMs":200,
        "requestTimeoutSeconds":10,"maxRunSeconds":100}"#;
    let file = TestFile::new(json);
    let common = Cli {
        username: Some("cli-user".into()),
        password: Some("cli-pass".into()),
        lessons: Some(4),
        delay_min_ms: Some(300),
        delay_max_ms: Some(400),
        timeout_seconds: Some(20),
        max_run_seconds: Some(200),
        ..Cli::default()
    };
    // Parse each invocation separately because Cli intentionally does not clone secrets.
    let args = [
        "lingosbot",
        "-u",
        "cli-user",
        "-p",
        "cli-pass",
        "--lessons",
        "4",
        "--delay-min-ms",
        "300",
        "--delay-max-ms",
        "400",
        "--timeout-seconds",
        "20",
        "--max-run-seconds",
        "200",
    ];
    let inline = Cli::try_parse_from(args.into_iter().chain(["--config-json", json]))
        .unwrap()
        .resolve()
        .unwrap();
    let from_stdin = Cli::try_parse_from(args.into_iter().chain(["--config-json-stdin"]))
        .unwrap()
        .resolve_with_stdin(json.as_bytes())
        .unwrap();
    let from_file = Cli {
        config: Some(file.0.clone()),
        ..common
    }
    .resolve()
    .unwrap();
    for config in [inline, from_stdin, from_file] {
        assert_eq!(config.username, "cli-user");
        assert_eq!(config.password, "cli-pass");
        assert_eq!(config.lesson_count, 4);
        assert_eq!(
            config.class_lesson_counts,
            BTreeMap::from([("group:42".to_owned(), 3), ("disabled".to_owned(), 0)])
        );
        assert_eq!(
            (config.answer_delay_min_ms, config.answer_delay_max_ms),
            (300, 400)
        );
        assert_eq!(config.request_timeout_seconds, 20);
        assert_eq!(config.max_run_seconds, 200);
    }
}

#[test]
fn json_sources_are_mutually_exclusive_even_for_directly_constructed_cli() {
    for args in [
        vec!["lingosbot", "--config", "path", "--config-json", "{}"],
        vec!["lingosbot", "--config", "path", "--config-json-stdin"],
        vec!["lingosbot", "--config-json", "{}", "--config-json-stdin"],
    ] {
        assert_eq!(
            Cli::try_parse_from(args).unwrap_err().kind(),
            ErrorKind::ArgumentConflict
        );
    }
    assert!(
        Cli {
            config: Some("nonexistent".into()),
            config_json_stdin: true,
            ..Cli::default()
        }
        .resolve_with_stdin(&b"{}"[..])
        .unwrap_err()
        .to_string()
        .contains("mutually exclusive")
    );
    assert!(
        Cli {
            config_json: Some("{}".into()),
            config_json_stdin: true,
            ..Cli::default()
        }
        .resolve_with_stdin(&b"{}"[..])
        .is_err()
    );
}

#[test]
fn json_sources_reject_invalid_and_oversized_input_without_leaking_secrets() {
    let secret = "unique-secret-in-inline-json";
    for json in [
        "null".to_owned(),
        "[]".to_owned(),
        format!(r#"{{"password":"{secret}","lessonCount":"{secret}"}}"#),
        format!(r#"{{"username":"{secret}","username":"other"}}"#),
    ] {
        for stdin in [false, true] {
            let cli = Cli {
                config_json: (!stdin).then(|| json.clone()),
                config_json_stdin: stdin,
                username: Some("user".into()),
                password: Some("password".into()),
                ..Cli::default()
            };
            assert!(!format!("{cli:?}").contains(secret));
            let error = cli.resolve_with_stdin(json.as_bytes()).unwrap_err();
            assert!(!format!("{error:?} {error}").contains(secret));
        }
    }
    let mut json = br#"{"username":"user","password":"secret"}"#.to_vec();
    json.resize(MAX_CONFIG_BYTES as usize, b' ');
    for stdin in [false, true] {
        let make_cli = |bytes: &[u8]| Cli {
            config_json: (!stdin).then(|| String::from_utf8(bytes.to_vec()).unwrap()),
            config_json_stdin: stdin,
            username: Some("user".into()),
            password: Some("password".into()),
            ..Cli::default()
        };
        assert!(make_cli(&json).resolve_with_stdin(json.as_slice()).is_ok());
        let mut oversized = json.clone();
        oversized.push(b' ');
        assert!(
            make_cli(&oversized)
                .resolve_with_stdin(oversized.as_slice())
                .unwrap_err()
                .to_string()
                .contains("1 MiB")
        );
    }
}

#[test]
fn stdin_is_only_read_when_requested_and_read_errors_are_sanitized() {
    struct FailingReader;
    impl std::io::Read for FailingReader {
        fn read(&mut self, _: &mut [u8]) -> std::io::Result<usize> {
            Err(std::io::Error::other("secret reader error"))
        }
    }
    let cli = Cli {
        username: Some("user".into()),
        password: Some("pass".into()),
        ..Cli::default()
    };
    assert!(cli.resolve_with_stdin(FailingReader).is_ok());
    let error = Cli {
        config_json_stdin: true,
        ..Cli::default()
    }
    .resolve_with_stdin(FailingReader)
    .unwrap_err();
    assert!(!format!("{error:?} {error}").contains("secret"));
}

#[test]
fn resolve_reads_json_from_actual_stdin() {
    use std::io::Write;
    use std::process::Stdio;

    const MARKER: &str = "LINGOS_CONFIG_STDIN_TEST_CHILD";
    if std::env::var_os(MARKER).is_some() {
        let config = Cli::try_parse_from([
            "lingosbot",
            "--config-json-stdin",
            "-u",
            "cli-user",
            "-p",
            "cli-pass",
        ])
        .unwrap()
        .resolve()
        .unwrap();
        assert_eq!(config.username, "cli-user");
        assert_eq!(config.class_lesson_counts["group:42"], 2);
        return;
    }
    let mut child = Command::new(std::env::current_exe().unwrap())
        .args(["resolve_reads_json_from_actual_stdin", "--test-threads=1"])
        .env(MARKER, "1")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(br#"{"classLessonCounts":{"group:42":2}}"#)
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(output.status.success(), "{output:?}");
    assert!(String::from_utf8_lossy(&output.stdout).contains("1 passed"));
}

#[test]
fn integration_flags_are_parsed_and_error_rate_overrides_json() {
    let json = r#"{"username":"user","password":"pass","errorsPer100Words":75}"#;
    let cli = Cli::try_parse_from([
        "lingosbot",
        "--config-json",
        json,
        "--realtime",
        "--scrape-class",
        "42",
        "--errors-per100-words",
        "0",
        "-u",
        "user",
        "-p",
        "pass",
    ])
    .unwrap();
    assert!(cli.realtime);
    assert_eq!(cli.scrape_class, Some(42));
    assert_eq!(cli.resolve().unwrap().errors_per100_words, 0);
    let cli = Cli::try_parse_from(["lingosbot", "--scrape-class", "-1"]).unwrap();
    assert_eq!(cli.scrape_class, Some(-1));
    for value in ["101", "256", "-1"] {
        let parsed = Cli::try_parse_from([
            "lingosbot",
            "-u",
            "user",
            "-p",
            "pass",
            "--errors-per100-words",
            value,
        ]);
        assert!(parsed.is_err() || parsed.unwrap().resolve().is_err());
    }
}

#[test]
fn missing_delay_bounds_adapt_without_increasing_the_minimum() {
    for (settings, expected) in [
        (serde_json::json!({}), (0, 0)),
        (serde_json::json!({"answerDelayMinMs": 250}), (250, 250)),
        (serde_json::json!({"answerDelayMaxMs": 250}), (0, 250)),
        (serde_json::json!({"answerDelayMinMs": 0}), (0, 0)),
        (serde_json::json!({"answerDelayMaxMs": 0}), (0, 0)),
        (
            serde_json::json!({"answerDelayMinMs": 800, "answerDelayMaxMs": 1800}),
            (800, 1800),
        ),
    ] {
        let mut value = serde_json::json!({"username": "user", "password": "secret"});
        value
            .as_object_mut()
            .unwrap()
            .extend(settings.as_object().unwrap().clone());
        let config = resolve_json(value).unwrap();
        assert_eq!(
            (config.answer_delay_min_ms, config.answer_delay_max_ms),
            expected
        );
    }

    for (flag, expected) in [("--delay-min-ms", (250, 250)), ("--delay-max-ms", (0, 250))] {
        let config = Cli::try_parse_from(["lingosbot", "-u", "user", "-p", "secret", flag, "250"])
            .unwrap()
            .resolve()
            .unwrap();
        assert_eq!(
            (config.answer_delay_min_ms, config.answer_delay_max_ms),
            expected
        );
    }
}

#[test]
fn explicit_delay_bounds_are_not_adjusted_across_cli_and_config() {
    let file = TestFile::new(
        r#"{"username":"user","password":"secret","answerDelayMinMs":100,"answerDelayMaxMs":200}"#,
    );
    let config = Cli {
        delay_min_ms: Some(0),
        ..file.cli()
    }
    .resolve()
    .unwrap();
    assert_eq!(config.answer_delay_min_ms, 0);
    assert_eq!(config.answer_delay_max_ms, 200);
    assert!(
        Cli {
            delay_min_ms: Some(300),
            ..file.cli()
        }
        .resolve()
        .is_err()
    );
    assert!(
        Cli {
            delay_max_ms: Some(0),
            ..file.cli()
        }
        .resolve()
        .is_err()
    );

    let file = TestFile::new(r#"{"username":"user","password":"secret","answerDelayMinMs":100}"#);
    let config = Cli {
        delay_min_ms: Some(250),
        ..file.cli()
    }
    .resolve()
    .unwrap();
    assert_eq!(config.answer_delay_min_ms, 250);
    assert_eq!(config.answer_delay_max_ms, 250);
}

#[test]
fn desktop_shape_and_unrelated_browser_fields_are_supported() {
    let config = resolve_json(serde_json::json!({
        "credentials": {"email": "  desktop@example.test  ", "password": " pass "},
        "browserBinaryPath": null, "browser": "Chrome", "headless": false,
        "useLessonApi": true, "mode": "api", "lessonCount": 2,
        "classLessonCounts": {"disabled": 0, "group:42": 3},
        "lessonPromptSafetyCap": null
    }))
    .unwrap();
    assert_eq!(config.username, "desktop@example.test");
    assert_eq!(config.password, " pass ");
    assert_eq!(config.class_lesson_counts["disabled"], 0);
    assert_eq!(config.class_lesson_counts["group:42"], 3);
    assert_eq!(config.lesson_prompt_safety_cap, 30);
}

#[test]
fn top_level_credentials_override_desktop_per_field() {
    let config = resolve_json(serde_json::json!({
        "username": "top-user",
        "credentials": {"email": "desktop-user", "password": "desktop-password"}
    }))
    .unwrap();
    assert_eq!(config.username, "top-user");
    assert_eq!(config.password, "desktop-password");
    let config = resolve_json(serde_json::json!({
        "password": "top-password",
        "credentials": {"email": "desktop-user", "password": "desktop-password"}
    }))
    .unwrap();
    assert_eq!(config.username, "desktop-user");
    assert_eq!(config.password, "top-password");
}

#[test]
fn numeric_login_strings_are_supported_without_coercing_json_numbers() {
    let config = resolve_json(serde_json::json!({
        "username": "  123456  ", "password": "secret"
    }))
    .unwrap();
    assert_eq!(config.username, "123456");
    assert!(
        resolve_json(serde_json::json!({
            "username": 123456, "password": "secret"
        }))
        .is_err()
    );
    assert!(
        resolve_json(serde_json::json!({
            "credentials": {"email": 123456, "password": "secret"}
        }))
        .is_err()
    );
}

#[test]
fn json_must_be_an_object_even_when_cli_provides_credentials() {
    for contents in [
        "null",
        "[]",
        "[null,null,null,null,null,null,null,null,null,null,null,null,null,null]",
        "123",
        "\"not-an-object\"",
    ] {
        let file = TestFile::new(contents);
        assert!(
            Cli {
                username: Some("user".into()),
                password: Some("secret".into()),
                ..file.cli()
            }
            .resolve()
            .is_err(),
            "accepted non-object JSON"
        );
    }
}

#[test]
fn credentials_must_be_an_object_and_duplicate_fields_are_rejected() {
    for contents in [
        r#"{"credentials":["user","password"]}"#,
        r#"{"username":"first","username":"second","password":"secret"}"#,
        r#"{"credentials":{"email":"user","password":"first","password":"second"}}"#,
        r#"{"username":"user","password":"secret","lessonCount":1,"lesson_count":2}"#,
    ] {
        let file = TestFile::new(contents);
        assert!(
            Cli {
                username: Some("user".into()),
                password: Some("secret".into()),
                ..file.cli()
            }
            .resolve()
            .is_err()
        );
    }
}

#[test]
fn snake_case_aliases_cover_all_settings() {
    let config = resolve_json(serde_json::json!({
        "username": "user", "password": "secret",
        "base_url": "https://example.test/",
        "student_dashboard_url": "/student/../student/dashboard",
        "lesson_count": 4, "class_lesson_counts": {"class": 5},
        "lesson_prompt_safety_cap": 40, "errors_per100_words": 2,
        "answer_delay_min_ms": 10, "answer_delay_max_ms": 20,
        "request_timeout_seconds": 40, "max_run_seconds": 400
    }))
    .unwrap();
    assert_eq!(config.base_url, "https://example.test");
    assert_eq!(
        config.student_dashboard_url,
        "https://example.test/student/dashboard"
    );
    assert_eq!(config.lesson_count, 4);
    assert_eq!(config.class_lesson_counts["class"], 5);
    assert_eq!(config.lesson_prompt_safety_cap, 40);
    assert_eq!(config.errors_per100_words, 2);
    assert_eq!(config.answer_delay_min_ms, 10);
    assert_eq!(config.answer_delay_max_ms, 20);
    assert_eq!(config.request_timeout_seconds, 40);
    assert_eq!(config.max_run_seconds, 400);
}

#[test]
fn missing_and_blank_credentials_are_rejected_without_falling_back() {
    for value in [
        serde_json::json!({}),
        serde_json::json!({"username": "user"}),
        serde_json::json!({"password": "secret"}),
        serde_json::json!({"username": " \t", "password": "secret"}),
        serde_json::json!({"username": "user", "password": " \n"}),
        serde_json::json!({"username": "", "credentials": {"email": "fallback", "password": "pass"}}),
        serde_json::json!({"password": "", "credentials": {"email": "fallback", "password": "pass"}}),
    ] {
        assert!(resolve_json(value).is_err());
    }
    let file = TestFile::new(r#"{"username":"user","password":"secret"}"#);
    assert!(
        Cli {
            password: Some(" ".into()),
            ..file.cli()
        }
        .resolve()
        .is_err()
    );
}

#[test]
fn malformed_json_and_incorrect_credential_types_do_not_leak_values() {
    let secret = "unique-sensitive-test-string";
    for contents in [
        format!(r#"{{"password":"{secret}","username":"user""#),
        format!(r#"{{"username":123,"password":"{secret}"}}"#),
        format!(r#"{{"username":"user","password":{{"value":"{secret}"}}}}"#),
        format!(r#"{{"credentials":{{"email":123,"password":"{secret}"}}}}"#),
        format!(r#"{{"credentials":"{secret}"}}"#),
        format!(r#"{{"username":"user","password":"secret","lessonCount":"{secret}"}}"#),
        format!(r#"{{"username":"user","password":"secret","mode":"{secret}"}}"#),
        format!(
            r#"{{"username":"user","password":"secret","baseUrl":"https://{secret}@example.test"}}"#
        ),
        "null".to_owned(),
        "[]".to_owned(),
        String::new(),
    ] {
        let error = TestFile::new(contents).cli().resolve().unwrap_err();
        assert!(!error.to_string().contains(secret));
        assert!(!format!("{error:?}").contains(secret));
    }
}

#[test]
fn missing_explicit_file_is_not_created() {
    let file = TestFile::new(b"");
    fs::remove_file(&file.0).unwrap();
    assert!(file.cli().resolve().is_err());
    assert!(!file.0.exists());
}

#[test]
fn run_config_alias_has_the_same_file_behavior_and_conflicts() {
    let file = TestFile::new(r#"{"username":"user","password":"pass","lessonCount":3}"#);
    let path = file.0.to_str().unwrap();
    let config = Cli::try_parse_from([
        "lingosbot",
        "--run-config",
        path,
        "-u",
        "cli-user",
        "-p",
        "cli-pass",
    ])
    .unwrap()
    .resolve()
    .unwrap();
    assert_eq!(config.username, "cli-user");
    assert_eq!(config.lesson_count, 3);
    assert!(
        Cli::try_parse_from(["lingosbot", "--run-config", path, "--config-json", "{}"]).is_err()
    );
    assert!(
        Cli::try_parse_from(["lingosbot", "--run-config", path, "--config-json-stdin"]).is_err()
    );
    fs::remove_file(&file.0).unwrap();
    let cli = Cli::try_parse_from(["lingosbot", "--run-config", path]).unwrap();
    assert!(cli.resolve().is_err());
    assert!(!file.0.exists());
}

#[test]
fn config_size_is_limited_to_one_mib() {
    let mut bytes = br#"{"username":"user","password":"secret"}"#.to_vec();
    bytes.resize(MAX_CONFIG_BYTES as usize, b' ');
    assert!(TestFile::new(&bytes).cli().resolve().is_ok());
    bytes.push(b' ');
    let error = TestFile::new(bytes).cli().resolve().unwrap_err();
    assert!(error.to_string().contains("1 MiB"));
}

#[cfg(unix)]
#[test]
fn symlink_configs_are_supported_but_nonregular_files_are_rejected() {
    use std::os::unix::fs::symlink;

    let file = TestFile::new(r#"{"username":"user","password":"secret"}"#);
    let link = TestFile::new(b"");
    fs::remove_file(&link.0).unwrap();
    symlink(&file.0, &link.0).unwrap();
    assert_eq!(link.cli().resolve().unwrap().username, "user");
    assert!(read_config(Path::new("/dev/null")).is_err());
    assert!(read_config(file.0.parent().unwrap()).is_err());
}

#[test]
fn only_api_or_unspecified_mode_is_supported() {
    for mode in [
        serde_json::Value::Null,
        serde_json::json!(""),
        serde_json::json!(" \t"),
        serde_json::json!("api"),
        serde_json::json!(" API "),
    ] {
        assert!(with_setting("mode", mode).is_ok());
    }
    for mode in [
        serde_json::json!("browser"),
        serde_json::json!("headless"),
        serde_json::json!(42),
    ] {
        assert!(with_setting("mode", mode).is_err());
    }
}

#[test]
fn default_dashboard_follows_normalized_base_origin() {
    let config = with_setting("baseUrl", serde_json::json!("HTTPS://EXAMPLE.TEST:443/")).unwrap();
    assert_eq!(config.base_url, "https://example.test");
    assert_eq!(
        config.student_dashboard_url,
        "https://example.test/student/dashboard"
    );
    for origin in [
        "http://localhost:8080",
        "http://127.0.0.1:1234",
        "http://127.4.5.6:8080",
        "http://[::1]:8080",
    ] {
        let config = with_setting("baseUrl", serde_json::json!(origin)).unwrap();
        assert_eq!(
            config.student_dashboard_url,
            format!("{origin}/student/dashboard")
        );
        assert!(config.validate().is_ok());
    }
}

#[test]
fn unsafe_or_non_origin_base_urls_are_rejected() {
    for origin in [
        "",
        "lingos.pl",
        "https:lingos.pl",
        "ftp://lingos.pl",
        "http://lingos.pl",
        "http://localhost.evil.test",
        "http://192.168.1.1",
        "http://0.0.0.0",
        "http://[::]",
        "https://user:secret@lingos.pl",
        "https://@lingos.pl",
        "https://lingos.pl/path",
        "https://lingos.pl/a/..",
        "https://lingos.pl?",
        "https://lingos.pl?token=secret",
        "https://lingos.pl#",
        "https://lingos.pl/#fragment",
        "https://lin\ngos.pl",
        "https://lingos.pl\\path",
    ] {
        assert!(
            with_setting("baseUrl", serde_json::json!(origin)).is_err(),
            "accepted {origin:?}"
        );
    }
}

#[test]
fn dashboard_accepts_same_origin_absolute_or_relative_urls_and_normalizes() {
    for dashboard in [
        "/student/../student/dashboard",
        "student/dashboard",
        "https://LINGOS.PL:443/student/dashboard",
    ] {
        let config = with_setting("studentDashboardUrl", serde_json::json!(dashboard)).unwrap();
        assert_eq!(
            config.student_dashboard_url,
            "https://lingos.pl/student/dashboard"
        );
    }
    for dashboard in [
        "",
        "https://evil.test/student/dashboard",
        "//evil.test/student/dashboard",
        "http://lingos.pl/student/dashboard",
        "https://lingos.pl:444/student/dashboard",
        "https://user:secret@lingos.pl/student/dashboard",
        "https://@lingos.pl/student/dashboard",
        "/student/dashboard?token=secret",
        "/student/dashboard#fragment",
        "javascript:alert(1)",
    ] {
        assert!(with_setting("studentDashboardUrl", serde_json::json!(dashboard)).is_err());
    }
}

#[test]
fn invalid_numeric_json_values_are_rejected() {
    for (name, values) in [
        (
            "lessonCount",
            vec![
                serde_json::json!(0),
                serde_json::json!(-1),
                serde_json::json!(10001),
                serde_json::json!(1.5),
                serde_json::json!(4294967296_u64),
            ],
        ),
        (
            "lessonPromptSafetyCap",
            vec![
                serde_json::json!(0),
                serde_json::json!(-1),
                serde_json::json!(10001),
            ],
        ),
        (
            "errorsPer100Words",
            vec![
                serde_json::json!(-1),
                serde_json::json!(101),
                serde_json::json!(256),
            ],
        ),
        (
            "answerDelayMinMs",
            vec![serde_json::json!(-1), serde_json::json!(60001)],
        ),
        (
            "answerDelayMaxMs",
            vec![serde_json::json!(-1), serde_json::json!(60001)],
        ),
        (
            "requestTimeoutSeconds",
            vec![
                serde_json::json!(0),
                serde_json::json!(-1),
                serde_json::json!(301),
            ],
        ),
        (
            "maxRunSeconds",
            vec![
                serde_json::json!(0),
                serde_json::json!(-1),
                serde_json::json!(86401),
            ],
        ),
    ] {
        for value in values {
            assert!(
                with_setting(name, value).is_err(),
                "accepted invalid {name}"
            );
        }
    }
    assert!(resolve_json(serde_json::json!({"username": "u", "password": "p", "answerDelayMinMs": 101, "answerDelayMaxMs": 100})).is_err());
}

#[test]
fn numeric_boundaries_and_disabled_class_overrides_are_valid() {
    let mut config = valid_config();
    config.lesson_count = MAX_LESSONS;
    config.class_lesson_counts.insert("disabled".into(), 0);
    config
        .class_lesson_counts
        .insert("class".into(), MAX_LESSONS);
    config.lesson_prompt_safety_cap = MAX_PROMPT_SAFETY_CAP;
    config.errors_per100_words = 100;
    config.answer_delay_min_ms = 0;
    config.answer_delay_max_ms = MAX_DELAY_MS;
    config.request_timeout_seconds = 300;
    config.max_run_seconds = 86_400;
    assert!(config.validate().is_ok());
    config.answer_delay_max_ms = 0;
    config.request_timeout_seconds = 1;
    config.max_run_seconds = 1;
    assert!(config.validate().is_ok());
}

#[test]
fn class_counts_require_nonblank_names_and_an_aggregate_budget() {
    for counts in [
        serde_json::json!({" ": 1}),
        serde_json::json!({"class": -1}),
        serde_json::json!({"class": 10001}),
        serde_json::json!({"class": 1.5}),
        serde_json::json!({"a": 6000, "b": 4001}),
    ] {
        assert!(with_setting("classLessonCounts", counts).is_err());
    }
    let mut config = valid_config();
    config.class_lesson_counts = (0..=MAX_LESSONS)
        .map(|index| (index.to_string(), 0))
        .collect();
    assert!(config.validate().is_err());
}

#[test]
fn validate_checks_directly_constructed_configs() {
    let mut config = valid_config();
    config.base_url = "http://example.test".into();
    assert!(config.validate().is_err());
    config = valid_config();
    config.student_dashboard_url = "https://evil.test/dashboard".into();
    assert!(config.validate().is_err());
    config = valid_config();
    config.lesson_count = 0;
    assert!(config.validate().is_err());
    config = valid_config();
    config.answer_delay_min_ms = u64::MAX;
    assert!(config.validate().is_err());
}

#[test]
fn debug_output_does_not_expose_credentials_or_other_secret_strings() {
    let secret = "unique-sensitive-test-string";
    let cli = Cli {
        username: Some(secret.into()),
        password: Some(secret.into()),
        config: Some(PathBuf::from(secret)),
        ..Cli::default()
    };
    let config = Config {
        username: secret.into(),
        password: secret.into(),
        base_url: secret.into(),
        student_dashboard_url: secret.into(),
        class_lesson_counts: BTreeMap::from([(secret.into(), 1)]),
        ..Config::default()
    };
    assert!(!format!("{cli:?}").contains(secret));
    assert!(!format!("{config:?}").contains(secret));
    assert!(format!("{config:?}").contains("redacted"));
}

#[test]
fn cli_parses_short_credentials_hyphen_passwords_and_extra_flags() {
    let cli = Cli::try_parse_from([
        "worker-server-rust",
        "-u",
        "user",
        "-p",
        "-starts-with-a-hyphen",
        "--json",
        "--lessons",
        "4",
        "--delay-min-ms",
        "0",
        "--delay-max-ms",
        "0",
        "--timeout-seconds",
        "1",
        "--max-run-seconds",
        "2",
    ])
    .unwrap();
    assert!(cli.json);
    let config = cli.resolve().unwrap();
    assert_eq!(config.password, "-starts-with-a-hyphen");
    assert_eq!(config.lesson_count, 4);
    assert_eq!(config.answer_delay_min_ms, 0);
    assert_eq!(config.answer_delay_max_ms, 0);
    assert_eq!(config.request_timeout_seconds, 1);
    assert_eq!(config.max_run_seconds, 2);
    assert!(Cli::try_parse_from(["worker-server-rust", "--lessons", "-1"]).is_err());
    assert!(
        Cli::try_parse_from(["worker-server-rust", "--base-url", "https://evil.test"]).is_err()
    );
}

#[test]
fn cli_help_and_automatic_version_are_available() {
    let help = Cli::try_parse_from(["worker-server-rust", "--help"]).unwrap_err();
    assert_eq!(help.kind(), ErrorKind::DisplayHelp);
    assert!(help.to_string().contains("LINGOS_PASSWORD"));
    assert_eq!(
        Cli::try_parse_from(["worker-server-rust", "--version"])
            .unwrap_err()
            .kind(),
        ErrorKind::DisplayVersion
    );
}

#[test]
fn cli_environment_variables_and_precedence() {
    // Isolate environment changes in a child process instead of invoking
    // unsafe set_var while other Rust tests may be reading the environment.
    const MARKER: &str = "LINGOS_CONFIG_TEST_CHILD";
    const PASSWORD: &str = "unique-password-only-in-child-environment";
    if std::env::var_os(MARKER).is_some() {
        let config = Cli::try_parse_from(["worker-server-rust"])
            .unwrap()
            .resolve()
            .unwrap();
        assert_eq!(config.username, "environment-user");
        assert_eq!(config.password, PASSWORD);
        let file = TestFile::new(r#"{"username":"file-user","password":"file-password"}"#);
        let config =
            Cli::try_parse_from(["worker-server-rust", "--config", file.0.to_str().unwrap()])
                .unwrap()
                .resolve()
                .unwrap();
        assert_eq!(config.username, "environment-user");
        assert_eq!(config.password, PASSWORD);
        let config = Cli::try_parse_from([
            "worker-server-rust",
            "-u",
            "explicit-user",
            "-p",
            "explicit-password",
        ])
        .unwrap()
        .resolve()
        .unwrap();
        assert_eq!(config.username, "explicit-user");
        assert_eq!(config.password, "explicit-password");
        for flag in ["--username=", "--password="] {
            assert!(
                Cli::try_parse_from(["worker-server-rust", flag])
                    .unwrap()
                    .resolve()
                    .is_err()
            );
        }
        let help = Cli::try_parse_from(["worker-server-rust", "--help"])
            .unwrap_err()
            .to_string();
        assert!(!help.contains(PASSWORD));
        assert!(!help.contains("environment-user"));
        return;
    }
    let output = Command::new(std::env::current_exe().unwrap())
        .args([
            "cli_environment_variables_and_precedence",
            "--test-threads=1",
        ])
        .env(MARKER, "1")
        .env("LINGOS_USERNAME", "  environment-user  ")
        .env("LINGOS_PASSWORD", PASSWORD)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "environment subprocess failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(String::from_utf8_lossy(&output.stdout).contains("1 passed"));
}

#[test]
fn empty_environment_credentials_do_not_override_json() {
    const MARKER: &str = "LINGOS_CONFIG_EMPTY_ENV_TEST_CHILD";
    if std::env::var_os(MARKER).is_some() {
        let file = TestFile::new(r#"{"username":"file-user","password":"file-password"}"#);
        let config = Cli::try_parse_from(["lingosbot", "--config", file.0.to_str().unwrap()])
            .unwrap()
            .resolve()
            .unwrap();
        assert_eq!(config.username, "file-user");
        assert_eq!(config.password, "file-password");
        for flag in ["--username=", "--password="] {
            assert!(
                Cli::try_parse_from(["lingosbot", "--config", file.0.to_str().unwrap(), flag])
                    .unwrap()
                    .resolve()
                    .is_err()
            );
        }
        return;
    }
    let output = Command::new(std::env::current_exe().unwrap())
        .args([
            "empty_environment_credentials_do_not_override_json",
            "--test-threads=1",
        ])
        .env(MARKER, "1")
        .env("LINGOS_USERNAME", "")
        .env("LINGOS_PASSWORD", "")
        .output()
        .unwrap();
    assert!(output.status.success(), "{output:?}");
    assert!(String::from_utf8_lossy(&output.stdout).contains("1 passed"));
}

#[cfg(unix)]
#[test]
fn nonunicode_environment_credentials_are_sanitized_and_cli_can_override_them() {
    use std::ffi::OsString;
    use std::os::unix::ffi::OsStringExt;

    const MARKER: &str = "LINGOS_CONFIG_NONUNICODE_ENV_TEST_CHILD";
    const SECRET: &str = "private-invalid-environment-credential";
    if std::env::var_os(MARKER).is_some() {
        let error = Cli::try_parse_from(["lingosbot"])
            .unwrap()
            .resolve()
            .unwrap_err();
        assert_eq!(error.exit_code(), 3);
        assert!(!format!("{error:?}").contains(SECRET));
        let config = Cli::try_parse_from([
            "lingosbot",
            "-u",
            "explicit-user",
            "-p",
            "explicit-password",
        ])
        .unwrap()
        .resolve()
        .unwrap();
        assert_eq!(config.username, "explicit-user");
        assert_eq!(config.password, "explicit-password");
        return;
    }
    let mut bytes = SECRET.as_bytes().to_vec();
    bytes.push(0xff);
    for name in ["LINGOS_USERNAME", "LINGOS_PASSWORD"] {
        let output = Command::new(std::env::current_exe().unwrap())
            .args([
                "nonunicode_environment_credentials_are_sanitized_and_cli_can_override_them",
                "--test-threads=1",
            ])
            .env(MARKER, "1")
            .env("LINGOS_USERNAME", "environment-user")
            .env("LINGOS_PASSWORD", "environment-password")
            .env(name, OsString::from_vec(bytes.clone()))
            .output()
            .unwrap();
        assert!(output.status.success(), "{output:?}");
        assert!(String::from_utf8_lossy(&output.stdout).contains("1 passed"));
    }
}
