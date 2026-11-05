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
    if output.status.success() {
        assert_eq!(value["success"], true);
        assert!(value["username"].is_string());
        assert!(value["duration_seconds"].as_f64().unwrap() >= 0.0);
        assert!(value["words_learned"].is_u64());
    } else {
        assert_eq!(value["success"], false);
        let mut keys: Vec<_> = value
            .as_object()
            .unwrap()
            .keys()
            .map(String::as_str)
            .collect();
        keys.sort_unstable();
        assert_eq!(keys, ["error_code", "message", "success"]);
    }
    assert!(!value["message"].as_str().unwrap().is_empty());
    (output, value)
}

#[derive(Default)]
struct Options {
    limit: bool,
    incomplete: bool,
    incomplete_group: Option<u64>,
    wrong_vocabulary_group: Option<u64>,
    unconfirmed_join: bool,
    unconfirmed_switch: bool,
    drop_answer: bool,
    drop_after_first: bool,
    missing_feedback: bool,
    answer_override: Option<Value>,
    answer_raw: Option<&'static str>,
    answer_login: bool,
    unknown_challenge: bool,
    legacy_switch: bool,
    empty_group: Option<u64>,
    limited_group: Option<u64>,
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
                    "lessonLimitReached":options.limit || options.limited_group == Some(group),
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
            ("GET", "/student/wordsets") => {
                let sets = if options.empty_group == Some(group) {
                    json!([])
                } else {
                    json!([{"id":55,"name":"Set","counts":{"total":if options.incomplete || options.incomplete_group == Some(group) {3} else {2}}}])
                };
                Reply::page(json!({"page":"wordsets","wordsets":{
                    "currentGroup":{"id":if options.wrong_vocabulary_group == Some(group) {99} else {group}},"wordsets":sets
                }}))
            }
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
                if options.drop_answer || (options.drop_after_first && word > 1) {
                    return None;
                }
                if options.answer_login {
                    return Some(Reply::html(
                        "<div id=app></div><input name=_csrf_token value=expired>",
                    ));
                }
                if let Some(raw) = options.answer_raw {
                    let mut reply = Reply::json(json!({}));
                    reply.body = raw.into();
                    return Some(reply);
                }
                if let Some(value) = &options.answer_override {
                    return Some(Reply::json(value.clone()));
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
fn list_classes_returns_one_json_document_without_mutations_or_scraping() {
    let server = site(Options::default());
    let config = TempConfig::new(&server.base, json!({}));
    let output = command()
        .arg("--config")
        .arg(&config.0)
        .arg("--list-classes")
        .output()
        .unwrap();
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let result: Value =
        serde_json::from_slice(&output.stdout).expect("stdout must contain only one JSON document");
    assert_eq!(result["success"], true);
    let classes = result["classes"].as_array().expect("missing classes array");
    assert_eq!(classes.len(), 2);
    for (id, title) in [("1", "A"), ("2", "B")] {
        let class = classes
            .iter()
            .find(|class| class["group_id"] == id)
            .expect("missing class or group_id is not a string");
        assert_eq!(class["title"], title);
    }
    assert!(count(&server, "/student/dashboard") > 0);
    assert_eq!(count(&server, "/learning"), 0);
    assert_eq!(count(&server, "/student/wordsets"), 0);
    assert_eq!(count(&server, "/student/context"), 0);
    assert_eq!(count(&server, "/student/group"), 0);
    assert_eq!(count(&server, "/student/challenges"), 0);
    assert!(!String::from_utf8_lossy(&output.stdout).contains("fake-secret"));
    assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
}

#[test]
fn list_classes_conflicts_with_scrape_class_before_login() {
    for flags in [
        ["--list-classes", "--scrape-class", "1"],
        ["--scrape-class", "1", "--list-classes"],
    ] {
        let server = site(Options::default());
        let config = TempConfig::new(&server.base, json!({}));
        let output = command()
            .arg("--config")
            .arg(&config.0)
            .arg("--json")
            .args(flags)
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(3));
        let result: Value = serde_json::from_slice(&output.stdout)
            .expect("stdout must contain only one JSON error document");
        assert_eq!(result["success"], false);
        assert_eq!(result["error_code"], "CONFIG_ERROR");
        assert_eq!(server.count(), 0);
    }
}

#[test]
fn inline_and_stdin_json_configs_run_without_a_config_file() {
    use std::io::Write;
    use std::process::Stdio;

    for stdin_config in [false, true] {
        let server = site(Options {
            limit: true,
            ..Default::default()
        });
        let config = json!({
            "baseUrl": server.base, "studentDashboardUrl": "/student/dashboard",
            "credentials": {"email": "test@example.org", "password": "fake-secret"},
            "answerDelayMinMs": 0, "answerDelayMaxMs": 0
        })
        .to_string();
        let mut cmd = command();
        cmd.arg("--json");
        if stdin_config {
            cmd.arg("--config-json-stdin").stdin(Stdio::piped());
        } else {
            cmd.args(["--config-json", &config]);
        }
        let mut child = cmd
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        if stdin_config {
            let mut stdin = child.stdin.take().unwrap();
            stdin.write_all(config.as_bytes()).unwrap();
            drop(stdin); // The config is read until EOF.
        }
        let output = child.wait_with_output().unwrap();
        assert_eq!(
            output.status.code(),
            Some(0),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let result: Value =
            serde_json::from_slice(&output.stdout).expect("stdout must contain only JSON");
        assert_eq!(result["success"], true);
        assert_eq!(result["username"], "test@example.org");
        assert_eq!(result["words_learned"], 0);
        assert!(count(&server, "/student/dashboard") > 0);
        assert!(!String::from_utf8_lossy(&output.stdout).contains("fake-secret"));
        assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
    }
}

#[test]
fn invalid_inline_and_stdin_json_produce_json_errors_without_secrets() {
    use std::io::Write;
    use std::process::Stdio;

    for stdin_config in [false, true] {
        let mut cmd = command();
        cmd.arg("--json");
        if stdin_config {
            cmd.arg("--config-json-stdin").stdin(Stdio::piped());
        } else {
            cmd.args(["--config-json", "fake-secret invalid JSON"]);
        }
        let mut child = cmd
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        if stdin_config {
            let mut stdin = child.stdin.take().unwrap();
            stdin.write_all(b"fake-secret invalid JSON").unwrap();
            drop(stdin);
        }
        let output = child.wait_with_output().unwrap();
        assert_eq!(output.status.code(), Some(3));
        let result: Value =
            serde_json::from_slice(&output.stdout).expect("stdout must contain only JSON");
        assert_eq!(result["success"], false);
        assert_eq!(result["error_code"], "CONFIG_ERROR");
        assert!(!String::from_utf8_lossy(&output.stdout).contains("fake-secret"));
        assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
    }
}

#[test]
fn realtime_flushes_api_events_before_finishing_and_emits_one_final_result() {
    use std::io::{BufRead, BufReader};
    use std::process::Stdio;
    use std::sync::mpsc;
    use std::thread;
    use std::time::{Duration, Instant};

    let (blocked_tx, blocked_rx) = mpsc::channel();
    let (release_tx, release_rx) = mpsc::channel();
    let server = Server::start(move |request, _| {
        Some(match (request.method.as_str(), request.path.as_str()) {
            ("GET", "/h/login") => Reply::html("<input name=_csrf_token value=token>"),
            ("POST", "/h/login") => {
                blocked_tx.send(()).unwrap();
                // Bound the wait even if the test or child fails.
                let _ = release_rx.recv_timeout(Duration::from_secs(10));
                Reply::redirect("/student/dashboard")
            }
            ("GET", "/student/dashboard") => Reply::page(json!({"page":"dashboard","dashboard":{
                "currentGroup":{"id":1},"groups":[{"id":1,"name":"A"}],
                "contexts":{},"lessonLimitReached":true,"challenges":[]
            }})),
            _ => panic!("Unexpected request {} {}", request.method, request.path),
        })
    });
    let config = TempConfig::new(&server.base, json!({}));
    let mut child = command()
        .arg("--config")
        .arg(&config.0)
        .arg("--realtime")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let stdout = child.stdout.take().unwrap();
    let (line_tx, line_rx) = mpsc::channel();
    let reader = thread::spawn(move || {
        let mut reader = BufReader::new(stdout);
        loop {
            let mut line = String::new();
            if reader.read_line(&mut line).unwrap() == 0 {
                break;
            }
            if line_tx.send(line).is_err() {
                break;
            }
        }
    });

    let blocked = blocked_rx.recv_timeout(Duration::from_secs(5)).is_ok();
    let deadline = Instant::now() + Duration::from_secs(2);
    let mut lines = Vec::new();
    let mut api_before_release = false;
    while blocked && Instant::now() < deadline {
        match line_rx.recv_timeout(deadline.saturating_duration_since(Instant::now())) {
            Ok(line) => {
                api_before_release |=
                    serde_json::from_str::<Value>(&line).is_ok_and(|v| v["type"] == "http_request");
                lines.push(line);
                if api_before_release {
                    break;
                }
            }
            Err(_) => break,
        }
    }
    let _ = release_tx.send(());
    let output = child.wait_with_output().unwrap();
    reader.join().unwrap();
    lines.extend(line_rx.try_iter());
    assert!(blocked, "child did not reach the blocked login request");
    assert!(
        api_before_release,
        "no flushed API event while the process was still running: {lines:?}"
    );
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let events: Vec<Value> = lines
        .iter()
        .map(|line| {
            assert!(
                line.ends_with('\n'),
                "NDJSON line is not newline-terminated: {line:?}"
            );
            serde_json::from_str(line).unwrap_or_else(|_| panic!("not a raw JSON line: {line}"))
        })
        .collect();
    assert!(events.iter().all(|event| event["type"].is_string()));
    assert!(events.iter().any(|event| event["type"] == "http_request"));
    assert_eq!(
        events
            .iter()
            .filter(|event| event["type"] == "result")
            .count(),
        1
    );
    assert_eq!(events.last().unwrap()["type"], "result");
    assert!(!lines.join("\n").contains("fake-secret"));
    assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
}

#[test]
fn realtime_reports_class_and_lesson_sequence_with_local_counts() {
    let server = site(Options::default());
    let config = TempConfig::new(
        &server.base,
        json!({"classLessonCounts":{"group:1":2,"group:2":1}}),
    );
    let events = run_realtime(&config, 0);
    let mut expected = Vec::new();
    for (class_id, class_title, total) in [("1", "A", 2), ("2", "B", 1)] {
        expected.push(json!({"type":"class_start", "class_id":class_id,
            "class_title":class_title, "lessons_total":total}));
        for lesson in 1..=total {
            expected.push(json!({"type":"lesson_start", "class_id":class_id,
                "class_title":class_title, "lesson":lesson, "lessons_total":total}));
            expected.push(json!({"type":"lesson_complete", "class_id":class_id,
                "class_title":class_title, "lesson":lesson, "lessons_total":total,
                "words_learned":1}));
        }
        expected.push(json!({"type":"class_complete", "class_id":class_id,
            "class_title":class_title, "lessons_completed":total}));
    }
    assert_eq!(progress_events(&events), expected);
    assert_eq!(events.last().unwrap()["lessons_completed"], 3);
    assert_eq!(events.last().unwrap()["words_learned"], 3);
    assert_eq!(count(&server, "/learning/start"), 3);
}

fn run_realtime(config: &TempConfig, exit_code: i32) -> Vec<Value> {
    let output = command()
        .arg("--config")
        .arg(&config.0)
        .arg("--realtime")
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(exit_code), "{output:?}");
    let stdout = std::str::from_utf8(&output.stdout).unwrap();
    assert!(!stdout.contains("fake-secret"));
    assert!(!stdout.contains("csrf&token"));
    assert!(!stdout.contains("\"answer\""));
    assert!(output.stderr.is_empty());
    let events: Vec<Value> = stdout
        .lines()
        .map(|line| serde_json::from_str(line).unwrap())
        .collect();
    assert_eq!(events.last().unwrap()["type"], "result");
    events
}

fn progress_events(events: &[Value]) -> Vec<Value> {
    events
        .iter()
        .filter(|event| {
            matches!(event["type"].as_str(), Some(kind) if kind.starts_with("class_")
                || matches!(kind, "lesson_start" | "lesson_complete"))
        })
        .cloned()
        .collect()
}

#[test]
fn realtime_reports_skips_and_never_completes_a_failed_lesson() {
    for (options, lessons_total, reason) in [
        (Options::default(), 0, "disabled"),
        (
            Options {
                empty_group: Some(1),
                ..Default::default()
            },
            1,
            "empty_vocabulary",
        ),
        (
            Options {
                limited_group: Some(1),
                ..Default::default()
            },
            1,
            "lesson_limit",
        ),
    ] {
        let server = site(options);
        let config = TempConfig::new(
            &server.base,
            json!({"classLessonCounts":{"group:1":lessons_total,"group:2":0}}),
        );
        let events = run_realtime(&config, 0);
        let mut expected = Vec::new();
        if lessons_total > 0 {
            expected.push(json!({"type":"class_start", "class_id":"1",
                "class_title":"A", "lessons_total":lessons_total}));
        }
        expected.push(json!({"type":"class_skipped", "class_id":"1",
            "class_title":"A", "reason":reason}));
        expected.push(json!({"type":"class_skipped", "class_id":"2",
            "class_title":"B", "reason":"disabled"}));
        assert_eq!(progress_events(&events), expected);
        assert_eq!(count(&server, "/learning"), 0);
    }

    let server = site(Options {
        drop_answer: true,
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({}));
    let events = run_realtime(&config, 2);
    assert_eq!(
        progress_events(&events),
        vec![
            json!({"type":"class_start", "class_id":"1", "class_title":"A", "lessons_total":1}),
            json!({"type":"lesson_start", "class_id":"1", "class_title":"A", "lesson":1, "lessons_total":1}),
        ]
    );
    assert_eq!(count(&server, "/learning/answer"), 1);
}

#[test]
fn realtime_config_failure_is_one_raw_result_event() {
    let output = command()
        .args(["--realtime", "--config-json", "invalid fake-secret"])
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(3));
    let stdout = std::str::from_utf8(&output.stdout).unwrap();
    assert!(
        stdout.ends_with('\n'),
        "NDJSON result must end with a newline"
    );
    let lines: Vec<_> = stdout.lines().collect();
    assert_eq!(lines.len(), 1, "{stdout}");
    let event: Value = serde_json::from_str(lines[0]).expect("result must be raw JSON");
    assert_eq!(event["type"], "result");
    assert!(!stdout.contains("fake-secret"));
    assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
}

#[test]
fn scrape_class_returns_words_grouped_by_wordset_without_lessons() {
    let server = Server::start(|request, _| {
        Some(match (request.method.as_str(), request.path.as_str()) {
            ("GET", "/h/login") => Reply::html("<input name=_csrf_token value=token>"),
            ("POST", "/h/login") => Reply::redirect("/student/dashboard"),
            ("GET", "/student/dashboard") => Reply::page(json!({"page":"dashboard","dashboard":{
                "currentGroup":{"id":1},"groups":[{"id":1,"name":"A"}],
                "contexts":{},"lessonLimitReached":false,"challenges":[]
            }})),
            ("GET", "/student/wordsets") => Reply::page(json!({"page":"wordsets","wordsets":{
                "currentGroup":{"id":1},"wordsets":[
                    {"id":55,"name":"First","counts":{"total":1}},
                    {"id":56,"name":"Second","counts":{"total":1}}
                ]
            }})),
            ("GET", "/student/wordsets/55") => Reply::page(json!({"page":"wordset","wordset":{
                "group":{"id":1},"wordset":{"id":55},"flashcards":[{"word":"house","translation":"dom"}]
            }})),
            ("GET", "/student/wordsets/56") => Reply::page(json!({"page":"wordset","wordset":{
                "group":{"id":1},"wordset":{"id":56},"flashcards":[{"word":"cat","translation":"kot"}]
            }})),
            _ => panic!("Unexpected request {} {}", request.method, request.path),
        })
    });
    let config = TempConfig::new(&server.base, json!({}));
    let output = command()
        .arg("--config")
        .arg(&config.0)
        .args(["--json", "--scrape-class", "1"])
        .output()
        .unwrap();
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let result: Value =
        serde_json::from_slice(&output.stdout).expect("stdout must contain only JSON");
    // Allow the CLI to wrap ScrapedClass in its success envelope.
    fn find_wordsets(value: &Value) -> Option<&Vec<Value>> {
        match value {
            Value::Object(map) => map
                .get("wordsets")
                .and_then(Value::as_array)
                .or_else(|| map.values().find_map(find_wordsets)),
            Value::Array(items) => items.iter().find_map(find_wordsets),
            _ => None,
        }
    }
    let sets = find_wordsets(&result).expect("scrape result must contain wordsets");
    assert_eq!(sets.len(), 2);
    for (name, word, translation) in [("First", "house", "dom"), ("Second", "cat", "kot")] {
        let set = sets
            .iter()
            .find(|set| set["info"]["name"] == name)
            .expect("missing wordset");
        let entries = set["entries"].as_array().expect("missing entries");
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0]["word"], word);
        assert_eq!(entries[0]["translation"], translation);
    }
    assert_eq!(count(&server, "/student/wordsets/55"), 1);
    assert_eq!(count(&server, "/student/wordsets/56"), 1);
    assert_eq!(count(&server, "/learning"), 0);
    assert_eq!(count(&server, "/student/challenges"), 0);
    assert!(!String::from_utf8_lossy(&output.stdout).contains("fake-secret"));
    assert!(!String::from_utf8_lossy(&output.stderr).contains("fake-secret"));
}

#[test]
fn scrape_all_collects_every_class_including_empty_without_lessons() {
    for realtime in [false, true] {
        let server = site(Options {
            empty_group: Some(1),
            limit: true,
            ..Default::default()
        });
        let config = TempConfig::new(
            &server.base,
            json!({"classLessonCounts":{"group:1":0,"group:2":0}}),
        );
        let mut cmd = command();
        cmd.arg("--config").arg(&config.0).arg("--scrape-all");
        if realtime {
            cmd.arg("--realtime");
        }
        let output = cmd.output().unwrap();
        assert_eq!(output.status.code(), Some(0), "{output:?}");
        let events: Vec<Value> = std::str::from_utf8(&output.stdout)
            .unwrap()
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
            .collect();
        let result = events.last().unwrap();
        assert_eq!(result["success"], true);
        assert_eq!(
            result["classes"],
            json!([{"group_id":"1","title":"A"},{"group_id":"2","title":"B"}])
        );
        assert_eq!(result["scrapes"].as_array().unwrap().len(), 2);
        assert_eq!(
            result["scrapes"][0],
            json!({"class":{"group_id":"1","title":"A"},"wordsets":[]})
        );
        assert_eq!(result["scrapes"][1]["class"], result["classes"][1]);
        assert_eq!(
            result["scrapes"][1]["wordsets"][0]["entries"]
                .as_array()
                .unwrap()
                .len(),
            2
        );
        assert!(result.get("scrape").is_none());
        if realtime {
            assert_eq!(
                events
                    .iter()
                    .filter(|event| event["type"] == "result")
                    .count(),
                1
            );
            assert_eq!(result["type"], "result");
        } else {
            assert_eq!(events.len(), 1);
        }
        assert_eq!(count(&server, "/learning"), 0);
        assert_eq!(count(&server, "/student/challenges"), 0);
        assert_eq!(count(&server, "/student/context"), 1);
    }
}

#[test]
fn scrape_all_discovers_freshly_including_zero_classes() {
    for empty in [false, true] {
        let mut dashboards = 0;
        let mut group = 1;
        let server = Server::start(move |request, _| {
            Some(match (request.method.as_str(), request.path.as_str()) {
                ("GET", "/h/login") => Reply::html("<input name=_csrf_token value=token>"),
                ("POST", "/h/login") => Reply::redirect("/student/dashboard"),
                ("GET", "/student/dashboard") => {
                    dashboards += 1;
                    let groups = if dashboards == 1 {
                        json!([{"id":1,"name":"Old"}])
                    } else if empty {
                        json!([])
                    } else {
                        json!([{"id":1,"name":"A"},{"id":2,"name":"B"}])
                    };
                    Reply::page(json!({"page":"dashboard","dashboard":{
                        "currentGroup":if empty && dashboards > 1 { Value::Null } else { json!({"id":group}) },
                        "groups":groups,"contexts":{},"lessonLimitReached":false,"challenges":[]
                    }}))
                }
                ("POST", "/student/context") => {
                    group = serde_json::from_str::<Value>(&request.body).unwrap()["id"]
                        .as_u64()
                        .unwrap();
                    Reply::json(json!({}))
                }
                ("GET", "/student/wordsets") => Reply::page(json!({"page":"wordsets","wordsets":{
                    "currentGroup":{"id":group},"wordsets":[]
                }})),
                _ => panic!("Unexpected request {} {}", request.method, request.path),
            })
        });
        let config = TempConfig::new(&server.base, json!({}));
        let output = command()
            .arg("--config")
            .arg(&config.0)
            .arg("--scrape-all")
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(0), "{output:?}");
        let result: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(result["success"], true);
        assert_eq!(
            result["classes"].as_array().unwrap().len(),
            if empty { 0 } else { 2 }
        );
        assert_eq!(
            result["scrapes"].as_array().unwrap().len(),
            if empty { 0 } else { 2 }
        );
        assert!(!String::from_utf8_lossy(&output.stdout).contains("Old"));
        assert!(count(&server, "/student/dashboard") >= 2);
        assert_eq!(
            count(&server, "/student/wordsets"),
            if empty { 0 } else { 2 }
        );
        assert_eq!(count(&server, "/learning"), 0);
        assert_eq!(count(&server, "/student/challenges"), 0);
    }
}

#[test]
fn scrape_all_rejects_wrong_class_and_partial_errors_without_returning_partial_scrapes() {
    for options in [
        Options {
            unconfirmed_switch: true,
            ..Default::default()
        },
        Options {
            incomplete_group: Some(2),
            ..Default::default()
        },
        Options {
            wrong_vocabulary_group: Some(2),
            ..Default::default()
        },
    ] {
        let server = site(options);
        let config = TempConfig::new(&server.base, json!({}));
        let output = command()
            .arg("--config")
            .arg(&config.0)
            .args(["--scrape-all", "--realtime"])
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(3), "{output:?}");
        let events: Vec<Value> = std::str::from_utf8(&output.stdout)
            .unwrap()
            .lines()
            .map(|line| serde_json::from_str(line).unwrap())
            .collect();
        let result = events.last().unwrap();
        assert_eq!(result["type"], "result");
        assert_eq!(result["success"], false);
        assert_eq!(result["error_code"], "PARSING_ERROR");
        assert!(result.get("classes").is_none());
        assert!(result.get("scrapes").is_none());
        assert_eq!(
            events
                .iter()
                .filter(|event| event["type"] == "result")
                .count(),
            1
        );
        assert!(count(&server, "/student/wordsets/55") >= 1);
        assert_eq!(count(&server, "/learning"), 0);
        assert_eq!(count(&server, "/student/challenges"), 0);
    }
}

#[test]
fn scrape_all_conflicts_with_list_and_single_class_before_login() {
    for flags in [
        vec!["--scrape-all", "--list-classes"],
        vec!["--list-classes", "--scrape-all"],
        vec!["--scrape-all", "--scrape-class", "1"],
        vec!["--scrape-class", "1", "--scrape-all"],
    ] {
        let server = site(Options::default());
        let config = TempConfig::new(&server.base, json!({}));
        let output = command()
            .arg("--config")
            .arg(&config.0)
            .args(flags)
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(3));
        let result: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(result["success"], false);
        assert_eq!(result["error_code"], "CONFIG_ERROR");
        assert_eq!(server.count(), 0);
    }
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
    assert_eq!(result["lessons_completed"], 4);
    let snapshots = result["challenge_snapshots"].as_array().unwrap();
    assert_eq!(snapshots.len(), 2);
    assert_eq!(snapshots[0]["group_id"], "1");
    assert_eq!(snapshots[0]["lessons_completed_at_snapshot"], 2);
    assert_eq!(snapshots[1]["group_id"], "2");
    assert_eq!(snapshots[1]["lessons_completed_at_snapshot"], 3);
    assert_eq!(result["challenge_snapshot"], snapshots[1]);
    for snapshot in snapshots {
        assert_eq!(snapshot["available"], 0);
        assert_eq!(snapshot["in_progress"], 1);
        assert_eq!(snapshot["completed"], 1);
        assert_eq!(snapshot["active_id"], "11");
        assert_eq!(snapshot["challenges"][0]["status"], "completed");
        assert_eq!(snapshot["challenges"][1]["status"], "in_progress");
    }
    assert!(result["duration_seconds"].as_f64().unwrap() >= 0.0);
    assert_eq!(count(&server, "/learning/start"), 4);
    assert_eq!(count(&server, "/learning/answer"), 10);
    assert_eq!(count(&server, "/student/challenges"), 4);
    assert_eq!(count(&server, "/student/context"), 1);
    // No extra dashboard GET immediately after login; post-lesson and
    // post-mutation confirmations are still fetched afresh.
    assert_eq!(count(&server, "/student/dashboard"), 11);
}

#[test]
fn complete_cli_handles_alternatives_new_cards_and_a_fresh_wordset_lesson_id() {
    let mut submission = 0;
    let exercise = |identifier, kind: &str, main: &str| {
        json!({"state":"exercise","exercise":{
            "identifier":identifier,"type":kind,"main":main
        }})
    };
    let server = Server::start(move |request, _| {
        Some(match (request.method.as_str(), request.path.as_str()) {
            ("GET", "/h/login") => Reply::html("<input name=_csrf_token value=token>"),
            ("POST", "/h/login") => Reply::redirect("/student/dashboard"),
            ("GET", "/student/dashboard") => Reply::page(json!({"page":"dashboard","dashboard":{
                "currentGroup":{"id":1},"groups":[{"id":1,"name":"A"}],
                "contexts":{},"lessonLimitReached":false,"challenges":[]
            }})),
            ("GET", "/student/wordsets") => Reply::page(json!({"page":"wordsets","wordsets":{
                "currentGroup":{"id":1},"wordsets":[{"id":55,"name":"Set","counts":{"total":2}}]
            }})),
            ("GET", "/student/wordsets/55") => Reply::page(json!({"page":"wordset","wordset":{
                "group":{"id":1},"wordset":{"id":55},
                "flashcards":[{"word":"house","translation":"dom"},{"word":"home","translation":"dom"}]
            }})),
            ("GET", "/learning/start/0?groupId=1") => Reply::page(json!({
                "status":"ok","lessonId":12,"step":exercise(1,"flashcard","dom")
            })),
            ("POST", "/learning/answer/12") => {
                let (expected, response) = match submission {
                    0 => (
                        json!({"identifier":1,"answer":"house","skip":false}),
                        json!({"feedback":{"result":0},"step":exercise(1,"flashcard","dom")}),
                    ),
                    1 => (
                        json!({"identifier":1,"answer":"home","skip":false}),
                        json!({"feedback":{"result":1},"step":exercise(2,"new_teacher","")}),
                    ),
                    2 => (
                        json!({"identifier":2,"answer":"","skip":false}),
                        json!({"step":exercise(3,"new_premium","")}),
                    ),
                    3 => (
                        json!({"identifier":3,"answer":"","skip":false}),
                        json!({"step":{"state":"change_wordset","continueUrl":"/learning/next?wordset=56"}}),
                    ),
                    _ => panic!("Unexpected submission to old lesson"),
                };
                assert_eq!(
                    serde_json::from_str::<Value>(&request.body).unwrap(),
                    expected
                );
                submission += 1;
                Reply::json(response)
            }
            ("GET", "/learning/next?wordset=56") => {
                assert_eq!(submission, 4);
                Reply::page(
                    json!({"status":"ok","lessonId":13,"step":exercise(1,"flashcard","house")}),
                )
            }
            ("POST", "/learning/answer/13") => {
                assert_eq!(submission, 4);
                assert_eq!(
                    serde_json::from_str::<Value>(&request.body).unwrap(),
                    json!({"identifier":1,"answer":"dom","skip":false})
                );
                submission += 1;
                Reply::json(json!({"feedback":{"result":2},"step":{"state":"finished"}}))
            }
            _ => panic!("Unexpected request {} {}", request.method, request.path),
        })
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, result) = run(&config);
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(result["words_learned"], 2);
    assert_eq!(count(&server, "/learning/start"), 1);
    assert_eq!(count(&server, "/learning/answer/12"), 4);
    assert_eq!(count(&server, "/learning/next"), 1);
    assert_eq!(count(&server, "/learning/answer/13"), 1);
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
fn a_limit_in_one_group_does_not_prevent_lessons_in_another() {
    let server = site(Options {
        limited_group: Some(1),
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(0));
    assert_eq!(result["words_learned"], 1);
    assert_eq!(count(&server, "/learning/start"), 1);
    assert_eq!(count(&server, "/student/context"), 1);
}

#[test]
fn a_valid_empty_class_does_not_block_the_next_class() {
    let server = site(Options {
        empty_group: Some(1),
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, result) = run(&config);
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(result["words_learned"], 1);
    assert_eq!(count(&server, "/learning/start"), 1);
    assert_eq!(count(&server, "/student/wordsets/55"), 1);
}

#[test]
fn no_vocabulary_is_reported_without_starting_an_empty_lesson() {
    let server = site(Options {
        empty_group: Some(1),
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({"classLessonCounts":{"group:2":0}}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(0));
    assert_eq!(result["words_learned"], 0);
    assert!(
        result["message"]
            .as_str()
            .unwrap()
            .contains("No vocabulary")
    );
    assert_eq!(count(&server, "/learning"), 0);
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
    assert_eq!(result["challenge_snapshots"].as_array().unwrap().len(), 1);
    assert_eq!(result["challenge_snapshots"][0]["group_id"], "1");
    assert_eq!(
        result["challenge_snapshots"][0]["lessons_completed_at_snapshot"],
        0
    );
    assert_eq!(
        result["challenge_snapshot"],
        result["challenge_snapshots"][0]
    );
    assert_eq!(count(&server, "/student/dashboard"), 1);
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
fn an_unconfirmed_switch_cannot_collect_vocabulary_or_start_a_lesson() {
    let server = site(Options {
        unconfirmed_switch: true,
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({"classLessonCounts":{"group:1":0}}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(3));
    assert_eq!(result["error_code"], "PARSING_ERROR");
    assert_eq!(count(&server, "/student/context"), 1);
    assert_eq!(count(&server, "/student/wordsets"), 0);
    assert_eq!(count(&server, "/student/challenges"), 0);
    assert_eq!(count(&server, "/learning"), 0);
}

#[test]
fn failure_after_a_confirmed_answer_returns_no_partial_statistics_or_replay() {
    let server = site(Options {
        drop_after_first: true,
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(2));
    assert_eq!(result["error_code"], "NETWORK_ERROR");
    assert_eq!(count(&server, "/learning/start"), 1);
    assert_eq!(count(&server, "/learning/answer"), 2);
    assert_eq!(count(&server, "/student/context"), 0);
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
fn positional_arrays_cannot_acknowledge_an_answer_or_trigger_an_alternative() {
    for payload in [
        json!({"step":["exercise",null,["flashcard",1,"dom"]],"feedback":[0]}),
        json!({"step":["finished",null,null],"feedback":{"result":1}}),
        json!({"step":{"state":"exercise","exercise":["flashcard",1,"dom"]},"feedback":{"result":0}}),
        json!({"step":{"state":"finished"},"feedback":[1]}),
    ] {
        let server = site(Options {
            answer_override: Some(payload),
            ..Default::default()
        });
        let config = TempConfig::new(&server.base, json!({}));
        let (output, result) = run(&config);
        assert_eq!(output.status.code(), Some(3));
        assert_eq!(result["error_code"], "PARSING_ERROR");
        assert_eq!(count(&server, "/learning/start"), 1);
        assert_eq!(count(&server, "/learning/answer"), 1);
        assert_eq!(count(&server, "/student/context"), 0);
    }
}

#[test]
fn duplicate_answer_fields_cannot_acknowledge_or_trigger_an_alternative() {
    for raw in [
        r#"{"feedback":{"result":0,"result":1},"step":{"state":"finished"}}"#,
        r#"{"feedback":{"result":0},"feedback":{"result":1},"step":{"state":"finished"}}"#,
        r#"{"feedback":{"result":0},"step":{"state":"finished"},"step":{"state":"exercise","exercise":{"type":"flashcard","identifier":1,"main":"dom"}}}"#,
    ] {
        let server = site(Options {
            answer_raw: Some(raw),
            ..Default::default()
        });
        let config = TempConfig::new(&server.base, json!({}));
        let (output, result) = run(&config);
        assert_eq!(output.status.code(), Some(3));
        assert_eq!(result["error_code"], "PARSING_ERROR");
        assert_eq!(count(&server, "/learning/answer"), 1);
        assert_eq!(count(&server, "/learning/start"), 1);
    }
}

#[test]
fn session_expiry_during_an_answer_is_auth_error_without_relogin_or_replay() {
    let server = site(Options {
        answer_login: true,
        ..Default::default()
    });
    let config = TempConfig::new(&server.base, json!({}));
    let (output, result) = run(&config);
    assert_eq!(output.status.code(), Some(1));
    assert_eq!(result["error_code"], "AUTH_FAILED");
    assert_eq!(count(&server, "/learning/answer"), 1);
    assert_eq!(count(&server, "/h/login"), 2);
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

#[cfg(unix)]
#[test]
fn fifo_config_is_rejected_without_waiting_for_a_writer() {
    use std::thread;
    use std::time::{Duration, Instant};

    let config = TempConfig::new("https://lingos.pl", json!({}));
    std::fs::remove_file(&config.0).unwrap();
    assert!(
        Command::new("mkfifo")
            .arg(&config.0)
            .status()
            .unwrap()
            .success()
    );
    let mut child = command()
        .arg("--config")
        .arg(&config.0)
        .arg("--json")
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(2);
    while child.try_wait().unwrap().is_none() {
        if Instant::now() >= deadline {
            child.kill().unwrap();
            let _ = child.wait();
            panic!("opening a FIFO config blocked the CLI");
        }
        thread::sleep(Duration::from_millis(10));
    }
    let output = child.wait_with_output().unwrap();
    assert_eq!(output.status.code(), Some(3));
    let result: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(result["error_code"], "CONFIG_ERROR");
}

#[cfg(target_os = "linux")]
#[test]
fn unwritable_stderr_cannot_abort_success_or_error_output() {
    for limit in [false, true] {
        let server = site(Options {
            limit,
            ..Default::default()
        });
        let config = TempConfig::new(&server.base, json!({}));
        let output = command()
            .arg("--config")
            .arg(&config.0)
            .arg("--json")
            .stderr(
                std::fs::OpenOptions::new()
                    .write(true)
                    .open("/dev/full")
                    .unwrap(),
            )
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(0));
        let result: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(result["success"], true);
    }

    let output = command()
        .arg("--unknown")
        .stderr(
            std::fs::OpenOptions::new()
                .write(true)
                .open("/dev/full")
                .unwrap(),
        )
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(3));
    assert!(output.stdout.is_empty());
}
