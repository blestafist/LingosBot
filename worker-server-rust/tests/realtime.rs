mod common;

use common::{Reply, Server};
use serde_json::{Value, json};
use std::io::{self, Write};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use worker_server_rust::{infrastructure::Session, lesson::LessonClient};

fn session(server: &Server) -> Session {
    Session::new(
        &server.base,
        Duration::from_secs(2),
        Duration::from_secs(10),
    )
    .unwrap()
}

#[derive(Default)]
struct Output {
    bytes: Vec<u8>,
    flushes: usize,
}

struct Writer(Arc<Mutex<Output>>);

impl Write for Writer {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        self.0.lock().unwrap().bytes.extend_from_slice(bytes);
        Ok(bytes.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        self.0.lock().unwrap().flushes += 1;
        Ok(())
    }
}

fn events(output: &Arc<Mutex<Output>>) -> Vec<Value> {
    let output = output.lock().unwrap();
    let text = std::str::from_utf8(&output.bytes).unwrap();
    assert!(text.is_empty() || text.ends_with('\n'));
    text.lines()
        .map(|line| serde_json::from_str(line).unwrap())
        .collect()
}

fn without_durations(mut events: Vec<Value>) -> Vec<Value> {
    for event in &mut events {
        if event["type"] == "http_request" {
            let duration = event
                .as_object_mut()
                .unwrap()
                .remove("duration_ms")
                .unwrap();
            assert!(duration.is_u64(), "duration_ms must be nonnegative integer");
        }
    }
    events
}

#[test]
fn ndjson_tracks_login_redirects_http_errors_and_flushes_without_secrets() {
    let server = Server::start(|_, index| {
        Some(match index {
            1 => Reply::html("<input name=_csrf_token value=token_secret>"),
            2 => Reply::redirect("/app?token=redirect_secret"),
            3 => Reply::page(json!({"page":"dashboard"})),
            4 => Reply::redirect("/next?token=next_secret"),
            5 => {
                let mut reply = Reply::html("response_secret");
                reply.status = 503;
                reply
            }
            _ => panic!("unexpected request"),
        })
    });
    let mut client = session(&server);
    let output = Arc::new(Mutex::new(Output::default()));
    client.set_ndjson_writer(Writer(Arc::clone(&output)));
    client
        .login(
            "user_secret",
            "password_secret",
            "/dashboard?token=dashboard_secret",
        )
        .unwrap();
    assert_eq!(client.api_call_count(), 3);
    assert!(client.get_page("/start?token=query_secret").is_err());
    assert_eq!(client.api_call_count(), 5);
    let lines = without_durations(events(&output));
    assert_eq!(lines.len(), 5);
    for (i, (method, status, operation)) in [
        ("GET", 200, "sign_in"),
        ("POST", 302, "sign_in"),
        ("GET", 200, "load_page"),
        ("GET", 302, "load_page"),
        ("GET", 503, "load_page"),
    ]
    .into_iter()
    .enumerate()
    {
        assert_eq!(
            lines[i],
            json!({"type":"http_request", "api_calls":i + 1, "method":method,
                "operation":operation, "status":status, "error":null})
        );
    }
    let output = output.lock().unwrap();
    assert_eq!(output.flushes, 5);
    let wire = std::str::from_utf8(&output.bytes).unwrap();
    assert!(!wire.contains("secret"));
    assert!(!wire.contains(&server.base));
}

#[test]
fn transport_failure_emits_one_event_and_preflight_failures_do_not_count() {
    let server = Server::start(|_, _| None);
    let mut client = session(&server);
    let output = Arc::new(Mutex::new(Output::default()));
    client.set_ndjson_writer(Writer(Arc::clone(&output)));
    assert!(client.post("https://example.org/secret", None).is_err());
    assert_eq!(client.api_call_count(), 0);
    assert!(
        client
            .post("/answer?token=secret", Some(&json!({"answer":"secret"})))
            .is_err()
    );
    assert_eq!(client.api_call_count(), 1);
    assert_eq!(server.count(), 1);
    assert_eq!(
        without_durations(events(&output)),
        vec![
            json!({"type":"http_request", "api_calls":1, "method":"POST",
                "operation":"load_page", "status":null, "error":"request"})
        ]
    );

    let mut expired = Session::new(&server.base, Duration::from_secs(1), Duration::ZERO).unwrap();
    expired.set_ndjson_writer(Writer(Arc::clone(&output)));
    assert!(expired.get_page("/never").is_err());
    assert_eq!(expired.api_call_count(), 0);
    assert_eq!(events(&output).len(), 1);
}

#[test]
fn lesson_and_application_events_are_opt_in_and_share_the_writer() {
    let server = Server::start(|_, _| Some(Reply::json(json!({}))));
    let mut client = session(&server);
    client.post("/before", None).unwrap();
    let output = Arc::new(Mutex::new(Output::default()));
    client.set_ndjson_writer(Writer(Arc::clone(&output)));
    client.emit_event(&json!({"type":"login_success"})).unwrap();
    client.progress("дом", true, false, 42).unwrap();
    client.post("/after", None).unwrap();
    client.clear_event_callback();
    client.progress("secret", false, true, 42).unwrap();
    client.post("/last", None).unwrap();
    assert_eq!(client.api_call_count(), 3);
    assert_eq!(
        without_durations(events(&output)),
        vec![
            json!({"type":"login_success"}),
            json!({"type":"lesson_answer", "word":"дом", "correct":true, "intentional":false, "words_learned":42}),
            json!({"type":"http_request", "api_calls":2, "method":"POST",
                "operation":"load_page", "status":200, "error":null}),
        ]
    );
    assert_eq!(output.lock().unwrap().flushes, 3);
}

#[test]
fn operations_are_fixed_labels_without_url_ids_headers_or_body_data() {
    let server = Server::start(|request, _| {
        Some(
            if request.method == "GET" {
                Reply::page(json!({"page":"ok"}))
            } else {
                Reply::json(json!({}))
            }
            .header("Set-Cookie", "session=header_secret; Path=/; HttpOnly"),
        )
    });
    let mut client = session(&server);
    let output = Arc::new(Mutex::new(Output::default()));
    client.set_ndjson_writer(Writer(Arc::clone(&output)));
    let cases = [
        ("POST", "/h/login", "sign_in"),
        ("GET", "/student/dashboard", "load_dashboard"),
        ("GET", "/student/wordsets", "load_vocabulary"),
        ("GET", "/student/wordsets/987654321", "load_vocabulary"),
        ("GET", "/learning/start/0", "start_lesson"),
        ("POST", "/learning/answer/987654321", "submit_answer"),
        ("GET", "/learning/next", "continue_lesson"),
        ("POST", "/student/context", "select_class"),
        ("POST", "/student/group", "select_class"),
        ("POST", "/student/challenges/987654321", "join_challenge"),
        ("GET", "/path_secret/987654321", "load_page"),
        ("POST", "/learning/answer/path_secret", "load_page"),
        (
            "GET",
            "/student/wordsets/987654321/path_secret",
            "load_page",
        ),
        ("GET", "/learning/answer/987654321", "load_page"),
        ("POST", "/student/dashboard", "load_page"),
    ];
    for (method, path, _) in cases {
        let url = format!("{path}?token=query_secret&groupId=987654321");
        if method == "GET" {
            client.get_page(&url).unwrap();
        } else {
            client
                .post(
                    &url,
                    Some(&json!({"answer":"body_secret","groupId":987654321})),
                )
                .unwrap();
        }
    }
    let lines = without_durations(events(&output));
    assert_eq!(lines.len(), cases.len());
    for (index, (method, _, operation)) in cases.into_iter().enumerate() {
        assert_eq!(
            lines[index],
            json!({"type":"http_request", "api_calls":index + 1,
            "method":method, "operation":operation, "status":200, "error":null})
        );
    }
    let output = output.lock().unwrap();
    let wire = std::str::from_utf8(&output.bytes).unwrap();
    assert!(!wire.contains("secret"));
    assert!(!wire.contains("987654321"));
    assert!(!wire.contains(&server.base));
    assert!(!wire.contains('/'));
}

#[test]
fn observer_failure_does_not_replay_a_post_or_leak_its_error() {
    let server = Server::start(|_, _| Some(Reply::json(json!({}))));
    let mut client = session(&server);
    client.set_event_callback(|_| Err(io::Error::other("writer secret")));
    let error = client.post("/answer", None).unwrap_err();
    assert!(!error.message.contains("secret"));
    assert_eq!(client.api_call_count(), 1);
    assert_eq!(server.count(), 1);
    assert!(client.emit_event(&json!({"type":"login_success"})).is_err());
    assert!(client.progress("secret", true, false, 1).is_err());
}

#[test]
fn event_is_delivered_before_response_body_is_available() {
    use std::io::Read;
    use std::net::TcpListener;
    use std::sync::mpsc;

    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let (tx, rx) = mpsc::channel();
    let server = std::thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(2)))
            .unwrap();
        let mut request = Vec::new();
        while !request.ends_with(b"\r\n\r\n") {
            let mut byte = [0];
            stream.read_exact(&mut byte).unwrap();
            request.push(byte[0]);
        }
        std::thread::sleep(Duration::from_millis(30));
        stream.write_all(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n").unwrap();
        rx.recv_timeout(Duration::from_secs(1))
            .expect("event must precede body");
        stream.write_all(b"{}").unwrap();
    });
    let mut client = Session::new(&base, Duration::from_secs(2), Duration::from_secs(5)).unwrap();
    client.set_event_callback(move |event| {
        assert_eq!(event["status"], 200);
        assert_eq!(event["operation"], "load_page");
        assert!(event["duration_ms"].as_u64().unwrap() >= 20);
        tx.send(()).unwrap();
        Ok(())
    });
    assert_eq!(client.post("/api", None).unwrap(), json!({}));
    server.join().unwrap();
}
