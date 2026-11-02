mod common;
use common::{Reply, Server};
use serde_json::{Value, json};
use std::path::PathBuf;
use std::process::{Command, Output};

struct TempConfig(PathBuf);
impl TempConfig {
    fn new(base: &str, extra: Value) -> Self {
        let path = std::env::temp_dir().join(format!(
            "lingos-test-{}-{}.json",
            std::process::id(),
            fastrand::u64(..)
        ));
        let mut value = json!({
            "baseUrl":base,"studentDashboardUrl":"/student/dashboard",
            "credentials":{"email":"test@example.org","password":"fake-secret"},
            "answerDelayMinMs":0,"answerDelayMaxMs":0
        });
        for (key, entry) in extra.as_object().unwrap() {
            value[key] = entry.clone();
        }
        std::fs::write(&path, value.to_string()).unwrap();
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
fn run(config: &TempConfig) -> (Output, Value) {
    let output = command()
        .arg("--config")
        .arg(&config.0)
        .arg("--json")
        .output()
        .unwrap();
    let stdout = std::str::from_utf8(&output.stdout).unwrap();
    let value: Value = serde_json::from_str(stdout).unwrap_or_else(|_| {
        panic!(
            "Not one JSON document: {stdout}; stderr: {}",
            String::from_utf8_lossy(&output.stderr)
        )
    });
    assert!(!stdout.contains("fake-secret"));
    assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
    (output, value)
}

#[derive(Default)]
struct Options {
    limit: bool,
    incomplete: bool,
    unconfirmed_join: bool,
    unconfirmed_switch: bool,
    drop_answer: bool,
    missing_feedback: bool,
    unknown_challenge: bool,
    legacy_switch: bool,
}
fn site(options: Options) -> Server {
    let mut group = 1;
    let mut joined = false;
    let mut training_joined = false;
    let mut completed = 0;
    let mut word = 1;
    Server::start(move |request, _| {
        let step = |word| {
            json!({"state":"exercise","exercise":{
                "identifier":word,"type":if word == 2 {"new_teacher"} else {"flashcard"},"main":"dom"
            }})
        };
        Some(match (request.method.as_str(), request.path.as_str()) {
            ("GET", "/h/login") => Reply::html("<input name=_csrf_token value='csrf&amp;token'>")
                .header("Set-Cookie", "session=test; Path=/; HttpOnly"),
            ("POST", "/h/login") => {
                assert!(request.body.contains("_password=fake-secret"));
                Reply::redirect("/student/dashboard")
            }
            ("GET", "/student/dashboard") => {
                let mut data = json!({
                    "currentGroup":{"id":group},"groups":[{"id":1,"name":"A"},{"id":2,"name":"B"}],
                    "lessonLimitReached":options.limit,
                    "challenges":[
                        {"id":10,"title":"Perfekcjonizm","description":"Wykonaj 1 lekcję bez błędów","prize":20,
                            "status":if options.unknown_challenge {"paused"} else if completed>0 {"completed"} else if joined {"in_progress"} else {"available"}},
                        {"id":11,"title":"Trening","description":"Ćwicz","prize":5,
                            "status":if training_joined {"in_progress"} else {"available"}}
                    ]
                });
                if !options.legacy_switch {
                    data["contexts"] = json!({});
                }
                Reply::page(json!({"page":"dashboard","dashboard":data}))
            }
            ("POST", "/student/context" | "/student/group") => {
                let body: Value = serde_json::from_str(&request.body).unwrap();
                let id = if options.legacy_switch {
                    body["groupId"].as_u64().unwrap()
                } else {
                    assert_eq!(body["kind"], "group");
                    body["id"].as_u64().unwrap()
                };
                if !options.unconfirmed_switch {
                    group = id;
                }
                joined = false;
                training_joined = false;
                completed = 0;
                Reply::json(json!({}))
            }
            ("GET", "/student/wordsets") => Reply::page(json!({"page":"wordsets","wordsets":{
                "currentGroup":{"id":group},"wordsets":[{"id":55,"name":"Set","counts":{"total":if options.incomplete {3} else {2}}}]
            }})),
            ("GET", "/student/wordsets/55") => Reply::page(json!({"page":"wordset","wordset":{
                "group":{"id":group},"wordset":{"id":55},"flashcards":[{"word":"house","translation":"dom"},{"word":"home","translation":"dom"}]
            }})),
            ("POST", "/student/challenges/10") => {
                joined = !options.unconfirmed_join;
                Reply::json(json!({}))
            }
            ("POST", "/student/challenges/11") => {
                training_joined = true;
                Reply::json(json!({}))
            }
            ("GET", path) if path.starts_with("/learning/start/0?") => {
                assert_eq!(path, format!("/learning/start/0?groupId={group}"));
                word = 1;
                Reply::page(json!({"status":"ok","lessonId":12,"step":step(word)}))
            }
            ("POST", "/learning/answer/12") => {
                if options.drop_answer {
                    return None;
                }
                let body: Value = serde_json::from_str(&request.body).unwrap();
                assert_eq!(body["identifier"].as_u64(), Some(word));
                assert_eq!(body["skip"], false);
                let accepted = word == 2 || body["answer"] == "house";
                if accepted {
                    word += 1;
                }
                if word == 3 {
                    completed += 1;
                }
                let next = if word == 3 {
                    json!({"state":"finished"})
                } else {
                    step(word)
                };
                let mut result = json!({"step":next});
                if !options.missing_feedback {
                    result["feedback"] = json!({"result":if accepted {1} else {0}});
                }
                Reply::json(result)
            }
            _ => panic!("Unexpected request {} {}", request.method, request.path),
        })
    })
}
fn count(server: &Server, prefix: &str) -> usize {
    server
        .requests
        .lock()
        .unwrap()
        .iter()
        .filter(|request| request.path.starts_with(prefix))
        .count()
}

#[test]
fn complete_cli_runs_all_classes_challenges_and_lessons() {
    let server = site(Options::default());
    let config = TempConfig::new(
        &server.base,
        json!({"lessonCount":2,"errorsPer100Words":100}),
    );
    let (output, result) = run(&config);
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(result["success"], true);
    assert_eq!(result["username"], "test@example.org");
    assert_eq!(result["words_learned"], 4);
    assert!(result["duration_seconds"].as_f64().unwrap() >= 0.0);
    assert_eq!(count(&server, "/learning/start"), 4);
    assert_eq!(count(&server, "/learning/answer"), 10);
    assert_eq!(count(&server, "/student/challenges"), 4);
    assert_eq!(count(&server, "/student/context"), 1);
}

#[test]
fn daily_limit_is_success_without_starting_or_collecting_vocabulary() {
    let server = site(Options {
        limit: true,
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(0));
    assert_eq!(result["words_learned"], 0);
    assert_eq!(count(&server, "/learning"), 0);
    assert_eq!(count(&server, "/student/wordsets"), 0);
}

#[test]
fn skipped_classes_are_never_switched_or_started() {
    let server = site(Options::default());
    let config = TempConfig::new(
        &server.base,
        json!({"classLessonCounts":{"group:1":0,"group:2":0}}),
    );
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(0));
    assert_eq!(result["words_learned"], 0);
    assert_eq!(count(&server, "/student/context"), 0);
    assert_eq!(count(&server, "/learning"), 0);
}

#[test]
fn discovered_defaults_cannot_exceed_total_lesson_budget() {
    let server = site(Options::default());
    let config = TempConfig::new(&server.base, json!({"lessonCount":6000}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(3));
    assert_eq!(result["error_code"], "CONFIG_ERROR");
    assert_eq!(count(&server, "/learning"), 0);
    assert_eq!(count(&server, "/student/context"), 0);
}

#[test]
fn case_ambiguous_class_overrides_are_rejected_before_login() {
    let server = site(Options::default());
    let config = TempConfig::new(
        &server.base,
        json!({"classLessonCounts":{"German":1,"german":2}}),
    );
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(3));
    assert_eq!(result["error_code"], "CONFIG_ERROR");
    assert_eq!(server.count(), 0);
}

#[test]
fn malformed_or_unconfirmed_state_stops_before_lesson() {
    for options in [
        Options {
            incomplete: true,
            ..Default::default()
        },
        Options {
            unconfirmed_join: true,
            ..Default::default()
        },
        Options {
            unconfirmed_switch: true,
            limit: true,
            ..Default::default()
        },
        Options {
            unknown_challenge: true,
            ..Default::default()
        },
    ] {
        let server = site(options);
        let config = TempConfig::new(&server.base, json!({}));
        let (output, result) = run(&config);
        assert_eq!(output.status.code(), Some(3));
        assert_eq!(result["error_code"], "PARSING_ERROR");
        assert_eq!(count(&server, "/learning"), 0);
    }
}

#[test]
fn ambiguous_or_invalid_answer_never_gets_replayed() {
    for (options, code, error_code) in [
        (
            Options {
                drop_answer: true,
                ..Default::default()
            },
            2,
            "NETWORK_ERROR",
        ),
        (
            Options {
                missing_feedback: true,
                ..Default::default()
            },
            3,
            "PARSING_ERROR",
        ),
    ] {
        let server = site(options);
        let config = TempConfig::new(&server.base, json!({}));
        let (output, result) = run(&config);
        assert_eq!(output.status.code(), Some(code));
        assert_eq!(result["error_code"], error_code);
        assert_eq!(count(&server, "/learning/answer"), 1);
    }
}

#[test]
fn legacy_group_switch_is_supported() {
    let server = site(Options {
        legacy_switch: true,
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, _) = run(&config);
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(count(&server, "/student/group"), 1);
}

#[test]
fn json_auth_and_config_failures_have_stable_exit_codes() {
    let server = Server::start(|_, index| {
        Some(if index == 1 {
            Reply::html("<input name=_csrf_token value=token>")
        } else {
            Reply::redirect("/h/login")
        })
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(1));
    assert_eq!(result["error_code"], "AUTH_FAILED");
    let config = TempConfig::new(&server.base, json!({"lessonCount":-1}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(3));
    assert_eq!(result["error_code"], "CONFIG_ERROR");
}

#[test]
fn help_version_and_argument_errors_do_not_require_credentials() {
    for flag in ["--help", "--version"] {
        assert!(command().arg(flag).output().unwrap().status.success());
    }
    let output = command()
        .args(["--json", "--unknown", "fake-secret"])
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(3));
    let result: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(result["success"], false);
    assert_eq!(result["error_code"], "CONFIG_ERROR");
    assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
}
