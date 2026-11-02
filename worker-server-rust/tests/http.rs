mod common;
use common::{Reply, Server};
use serde_json::json;
use std::time::Duration;
use worker_server_rust::{error::ErrorKind, infrastructure::Session};

fn session(server: &Server) -> Session {
    Session::new(
        &server.base,
        Duration::from_secs(2),
        Duration::from_secs(10),
    )
    .unwrap()
}

#[test]
fn login_posts_encoded_csrf_and_carries_rotating_cookies() {
    let server = Server::start(|request, index| {
        Some(match index {
            1 => {
                assert_eq!(request.path, "/h/login");
                Reply::html("<input value='a&amp;b' name='_csrf_token'>")
                    .header("Set-Cookie", "session=initial; Path=/; HttpOnly")
            }
            2 => {
                assert_eq!(request.method, "POST");
                assert!(request.headers.contains("session=initial"));
                assert!(request.body.contains("_csrf_token=a%26b"));
                assert!(request.body.contains("_username=a%40b.pl"));
                assert!(request.body.contains("_password=secret%26value"));
                Reply::redirect("/app/start")
                    .header("Set-Cookie", "session=authenticated; Path=/; HttpOnly")
            }
            3 => {
                assert_eq!(request.path, "/student/dashboard");
                assert!(request.headers.contains("session=authenticated"));
                Reply::page(json!({"page":"dashboard"}))
            }
            _ => panic!("Unexpected request"),
        })
    });
    session(&server)
        .login("a@b.pl", "secret&value", "/student/dashboard")
        .unwrap();
    assert_eq!(server.count(), 3);
}

#[test]
fn failed_login_does_not_request_dashboard() {
    let server = Server::start(|_, index| {
        Some(if index == 1 {
            Reply::html("<input name=_csrf_token value=token>")
        } else {
            Reply::redirect("/h/login?failed=1")
        })
    });
    let error = session(&server)
        .login("user", "secret", "/student/dashboard")
        .unwrap_err();
    assert_eq!(error.kind, ErrorKind::Auth);
    assert_eq!(server.count(), 2);
}

#[test]
fn login_distinguishes_rejected_credentials_http_errors_and_changed_pages() {
    for (status, body, kind) in [
        (200, "<input name=_csrf_token value=token>", ErrorKind::Auth),
        (200, "<html>changed-login-page</html>", ErrorKind::Parsing),
        (204, "", ErrorKind::Parsing),
        (400, "", ErrorKind::Network),
        (401, "", ErrorKind::Auth),
        (403, "", ErrorKind::Auth),
        (404, "", ErrorKind::Network),
        (429, "", ErrorKind::Network),
        (503, "", ErrorKind::Network),
    ] {
        let server = Server::start(move |_, index| {
            Some(if index == 1 {
                Reply::html("<input name=_csrf_token value=token>")
            } else {
                let mut reply = Reply::html(body);
                reply.status = status;
                reply
            })
        });
        let error = session(&server)
            .login("user", "secret", "/student/dashboard")
            .unwrap_err();
        assert_eq!(error.kind, kind, "HTTP {status}");
        assert!(!error.message.contains("changed-login-page"));
        assert_eq!(server.count(), 2);
    }
}

#[test]
fn direct_sessions_enforce_the_same_safe_origin_rules_as_the_cli() {
    for base in [
        "http://example.test",
        "http://localhost.evil.test",
        "https://user:secret@example.test",
        "https://@example.test",
        "https://example.test/path",
        "https://example.test/a/..",
        "https://example.test?",
        "https://example.test#fragment",
    ] {
        let result = Session::new(base, Duration::from_secs(1), Duration::from_secs(2));
        assert_eq!(result.err().unwrap().kind, ErrorKind::Config, "{base}");
    }
}

#[test]
fn expired_session_is_auth_error() {
    let server = Server::start(|_, _| Some(Reply::redirect("/h/login")));
    assert_eq!(
        session(&server)
            .get_page("/student/dashboard")
            .unwrap_err()
            .kind,
        ErrorKind::Auth
    );
    assert_eq!(server.count(), 1);
}

#[test]
fn relative_post_login_redirect_is_auth_error_without_following_it() {
    let server = Server::start(|_, _| Some(Reply::redirect("login")));
    let error = session(&server).post("/h/action", None).unwrap_err();
    assert_eq!(error.kind, ErrorKind::Auth);
    assert_eq!(server.count(), 1);
}

#[test]
fn slow_response_body_is_bounded_by_request_and_global_deadlines() {
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;

    for (request_timeout, run_timeout) in [
        (Duration::from_millis(30), Duration::from_secs(2)),
        (Duration::from_secs(2), Duration::from_millis(30)),
    ] {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let worker = thread::spawn(move || {
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
            stream.write_all(b"HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 1\r\nConnection: close\r\n\r\n").unwrap();
            thread::sleep(Duration::from_millis(120));
            let _ = stream.write_all(b"x");
        });
        let session = Session::new(&base, request_timeout, run_timeout).unwrap();
        let error = session.get_page("/slow").unwrap_err();
        worker.join().unwrap();
        assert_eq!(error.kind, ErrorKind::Network);
    }
}

#[test]
fn foreign_urls_and_redirects_cannot_receive_cookies() {
    let server = Server::start(|_, _| Some(Reply::redirect("https://example.org/steal")));
    let session = session(&server);
    for target in [
        "https://example.org",
        "http://user@127.0.0.1/",
        "//example.org/x",
    ] {
        assert!(session.get_page(target).is_err());
        assert!(session.post(target, None).is_err());
    }
    assert_eq!(server.count(), 0);
    assert!(session.get_page("/start").is_err());
    assert_eq!(server.count(), 1);
}

#[test]
fn relative_redirect_is_resolved_against_current_url() {
    let server = Server::start(|request, index| {
        Some(if index == 1 {
            Reply::redirect("next")
        } else {
            assert_eq!(request.path, "/nested/next");
            Reply::page(json!({"ok":true}))
        })
    });
    assert_eq!(
        session(&server).get_page("/nested/start").unwrap(),
        json!({"ok":true})
    );
}

#[test]
fn ambiguous_post_connection_drop_is_not_retried() {
    let server = Server::start(|_, _| None);
    let error = session(&server)
        .post("/learning/answer/12", Some(&json!({"answer":"secret"})))
        .unwrap_err();
    assert_eq!(error.kind, ErrorKind::Network);
    assert!(!error.message.contains("secret"));
    assert_eq!(server.count(), 1);
}

#[test]
fn post_redirect_and_invalid_response_are_never_replayed() {
    for reply_kind in 0..4 {
        let server = Server::start(move |_, _| {
            Some(match reply_kind {
                0 => Reply::redirect("/answer-again"),
                1 => Reply::html("<html>Unexpected login</html>"),
                2 => Reply {
                    status: 200,
                    headers: vec![("Content-Type".into(), "text/plain".into())],
                    body: "{}".into(),
                },
                _ => Reply::json(json!(null)),
            })
        });
        assert!(session(&server).post("/answer", None).is_err());
        assert_eq!(server.count(), 1);
    }
}

#[test]
fn oversized_body_stops_before_reading_it() {
    let server = Server::start(|_, _| Some(Reply::html("").header("Content-Length", "999999999")));
    assert!(session(&server).get_page("/large").is_err());
}

#[test]
fn global_deadline_covers_delays_without_sleeping_past_it() {
    let server = Server::start(|_, _| panic!("No request expected"));
    let session = Session::new(
        &server.base,
        Duration::from_secs(1),
        Duration::from_millis(10),
    )
    .unwrap();
    assert_eq!(
        session.wait(Duration::from_secs(1)).unwrap_err().kind,
        ErrorKind::Network
    );
    assert_eq!(server.count(), 0);
}
