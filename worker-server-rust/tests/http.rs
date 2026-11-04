mod common;
use common::{Reply, Server};
use serde_json::json;
use std::time::Duration;
use worker_server_rust::{
    error::ErrorKind, infrastructure::Session, lesson::LessonClient, models::AnswerRequest,
};

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

fn raw_json(body: impl Into<String>) -> Reply {
    Reply {
        status: 200,
        headers: vec![("Content-Type".into(), "application/json".into())],
        body: body.into(),
    }
}

fn raw_page(body: &str) -> Reply {
    Reply::html(format!(
        "<div id='app' data-props=\"{}\"></div>",
        html_escape::encode_double_quoted_attribute(body)
    ))
}

#[test]
fn api_headers_are_same_origin_and_identify_the_worker() {
    let server = Server::start(|request, _| {
        let header = |name: &str| {
            request.headers.lines().find_map(|line| {
                let (key, value) = line.split_once(':')?;
                key.eq_ignore_ascii_case(name).then_some(value.trim())
            })
        };
        let origin = format!("http://{}", header("Host").unwrap());
        assert_eq!(header("Origin"), Some(origin.as_str()));
        assert_eq!(header("Referer"), Some(format!("{origin}/").as_str()));
        assert_eq!(header("X-Requested-With"), Some("XMLHttpRequest"));
        assert_eq!(header("Accept"), Some("application/json"));
        assert_eq!(header("Accept-Encoding"), Some("gzip"));
        assert_eq!(header("Content-Type"), Some("application/json"));
        assert_eq!(
            header("User-Agent"),
            Some(concat!(
                "Mozilla/5.0 (X11; Linux x86_64) LingosBot/",
                env!("CARGO_PKG_VERSION")
            ))
        );
        Some(Reply::json(json!({"ok": true})))
    });
    session(&server)
        .post("/api", Some(&json!({"answer": "value"})))
        .unwrap();
    assert_eq!(server.count(), 1);
}

#[test]
fn duplicate_keys_are_rejected_before_value_can_hide_them() {
    for raw in [
        r#"{"step":{"state":"wrong"},"step":{"state":"complete"}}"#,
        r#"{"step":{"state":"wrong","state":"complete"}}"#,
        r#"{"step":{"state":"complete"},"unknown":{"x":1,"x":2}}"#,
        r#"{"step":{"state":"complete"},"unknown":[{"x":1,"\u0078":2}]}"#,
        r#"{"step":{"state":"complete"},"unknown":1,"unknown":2}"#,
    ] {
        let server = Server::start(move |_, _| Some(raw_json(raw)));
        let error = session(&server)
            .answer(
                12,
                &AnswerRequest {
                    identifier: 7,
                    answer: "secret".into(),
                    skip: false,
                },
            )
            .unwrap_err();
        assert_eq!(error.kind, ErrorKind::Parsing, "{raw}");
        assert!(!error.message.contains("secret"));
        assert_eq!(server.count(), 1);
    }
}

#[test]
fn html_page_duplicates_are_rejected_end_to_end() {
    for raw in [
        r#"{"lessonId":1,"lessonId":2}"#,
        r#"{"lessonId":1,"step":{"state":"wrong","state":"complete"}}"#,
        r#"{"lessonId":1,"unknown":[{"x":1,"\u0078":2}]}"#,
    ] {
        let server = Server::start(move |_, _| Some(raw_page(raw)));
        let error = session(&server).navigate("/learning/12").unwrap_err();
        assert_eq!(error.kind, ErrorKind::Parsing, "{raw}");
        assert_eq!(server.count(), 1);
    }
}

#[test]
fn json_nesting_is_bounded_for_api_and_html_including_error_cleanup() {
    for html in [false, true] {
        for depth in [1, 63, 64, 65, 128, 4096] {
            let raw = format!(
                "{{\"nested\":{}0{}}}",
                "[".repeat(depth - 1),
                "]".repeat(depth - 1)
            );
            let server = Server::start(move |_, _| {
                Some(if html {
                    raw_page(&raw)
                } else {
                    raw_json(raw.clone())
                })
            });
            let client = session(&server);
            let result = if html {
                client.get_page("/page")
            } else {
                client.post("/api", None)
            };
            if depth <= 64 {
                drop(result.unwrap());
            } else {
                assert_eq!(result.unwrap_err().kind, ErrorKind::Parsing);
            }
            assert_eq!(server.count(), 1);
        }

        // A fully allocated deep Value must also be safely dropped when a later
        // sibling fails validation (not just while descending into empty arrays).
        let raw = format!(
            "{{\"retained\":{}0{},\"bad\":{}0{}}}",
            "[".repeat(63),
            "]".repeat(63),
            "[".repeat(64),
            "]".repeat(64)
        );
        let server = Server::start(move |_, _| {
            Some(if html {
                raw_page(&raw)
            } else {
                raw_json(raw.clone())
            })
        });
        let client = session(&server);
        let result = if html {
            client.get_page("/page")
        } else {
            client.post("/api", None)
        };
        assert_eq!(result.unwrap_err().kind, ErrorKind::Parsing);
        assert_eq!(server.count(), 1);
    }
}

#[test]
fn strict_json_preserves_values_and_rejects_trailing_data() {
    let expected = json!({
        "null": null,
        "bool": true,
        "negative": -123,
        "large": u64::MAX,
        "fraction": 1.25,
        "text": "Zażółć & < >",
        "array": [null, false, {}, [1]],
        "separate": [{"same": 1}, {"same": 2}]
    });
    let reply = expected.clone();
    let server = Server::start(move |_, _| Some(Reply::json(reply.clone())));
    assert_eq!(session(&server).post("/api", None).unwrap(), expected);
    for raw in ["{} {}", "{} secret", r#"{"x":NaN}"#, r#"{"x":1e9999}"#] {
        let server = Server::start(move |_, _| Some(raw_json(raw)));
        let error = session(&server).post("/api", None).unwrap_err();
        assert_eq!(error.kind, ErrorKind::Parsing);
        assert!(!error.message.contains("secret"));
        assert_eq!(server.count(), 1);
    }
}

#[test]
fn json_node_budget_also_applies_to_unknown_fields_over_http() {
    for html in [false, true] {
        for count in [16_382, 16_383] {
            // Root object + unknown array + its scalar elements.
            let raw = format!(r#"{{"unknown":[{}]}}"#, vec!["0"; count].join(","));
            let server = Server::start(move |_, _| {
                Some(if html {
                    raw_page(&raw)
                } else {
                    raw_json(raw.clone())
                })
            });
            let client = session(&server);
            let result = if html {
                client.get_page("/page")
            } else {
                client.post("/api", None)
            };
            if count == 16_382 {
                assert_eq!(result.unwrap()["unknown"].as_array().unwrap().len(), count);
            } else {
                assert_eq!(result.unwrap_err().kind, ErrorKind::Parsing);
            }
            assert_eq!(server.count(), 1);
        }
    }
}

#[test]
fn expired_post_html_is_auth_only_for_a_valid_csrf_form() {
    for (body, kind) in [
        (
            "<form><input name='_csrf_token' value='token'></form>",
            ErrorKind::Auth,
        ),
        ("<html>login failed: secret</html>", ErrorKind::Parsing),
        ("<input name='_csrf_token'>", ErrorKind::Parsing),
        ("<input name='_csrf_token' value=''>", ErrorKind::Parsing),
    ] {
        let server = Server::start(move |_, _| Some(Reply::html(body)));
        let error = session(&server).post("/answer", None).unwrap_err();
        assert_eq!(error.kind, kind);
        assert!(!error.message.contains("secret"));
        assert_eq!(server.count(), 1);
    }
}

#[test]
fn login_wrong_dashboard_schema_is_parsing_not_bad_credentials() {
    for raw in [r#"{"page":"learning"}"#, r#"{"page":null}"#, "{}"] {
        let server = Server::start(move |_, index| {
            Some(match index {
                1 => Reply::html("<input name=_csrf_token value=token>"),
                2 => Reply::redirect("/student/dashboard"),
                3 => raw_page(raw),
                _ => panic!("Unexpected replay"),
            })
        });
        let error = session(&server)
            .login("user", "secret", "/student/dashboard")
            .unwrap_err();
        assert_eq!(error.kind, ErrorKind::Parsing);
        assert_eq!(server.count(), 3);
    }
}

#[test]
fn http_errors_do_not_retry_get_or_replay_post_even_with_retry_after() {
    for method in ["GET", "POST"] {
        for status in [401, 403, 429, 502, 503, 504] {
            let server = Server::start(move |request, _| {
                assert_eq!(request.method, method);
                let mut reply =
                    Reply::html("upstream diagnostic secret").header("Retry-After", "0");
                reply.status = status;
                Some(reply)
            });
            let client = session(&server);
            let error = if method == "GET" {
                client.get_page("/page")
            } else {
                client.post("/answer", Some(&json!({"answer":"secret"})))
            }
            .unwrap_err();
            assert_eq!(
                error.kind,
                if matches!(status, 401 | 403) {
                    ErrorKind::Auth
                } else {
                    ErrorKind::Network
                }
            );
            assert!(!error.message.contains("secret"));
            assert_eq!(server.count(), 1, "{method} HTTP {status}");
        }
    }
}

#[test]
fn get_only_follows_supported_redirect_statuses() {
    for status in [301, 302, 303, 307, 308] {
        let server = Server::start(move |request, index| {
            assert_eq!(request.method, "GET");
            Some(if index == 1 {
                let mut reply = Reply::redirect("/next");
                reply.status = status;
                reply
            } else {
                assert_eq!(request.path, "/next");
                Reply::page(json!({"ok":true}))
            })
        });
        assert_eq!(
            session(&server).get_page("/start").unwrap(),
            json!({"ok":true})
        );
        assert_eq!(server.count(), 2);
    }
    for status in [300, 304, 305, 306] {
        let server = Server::start(move |_, _| {
            let mut reply = Reply::redirect("/never-follow");
            reply.status = status;
            Some(reply)
        });
        let error = session(&server).get_page("/start").unwrap_err();
        assert_eq!(error.kind, ErrorKind::Network, "HTTP {status}");
        assert_eq!(server.count(), 1);
    }
}

#[test]
fn redirect_loops_stop_after_five_hops_and_post_never_follows() {
    let server = Server::start(|_, _| Some(Reply::redirect("/loop")));
    assert_eq!(
        session(&server).get_page("/loop").unwrap_err().kind,
        ErrorKind::Network
    );
    assert_eq!(server.count(), 6);

    for status in [301, 302, 303, 307, 308] {
        for location in ["/loop", "https://example.org/h/login", "http://[invalid"] {
            let server = Server::start(move |_, _| {
                let mut reply = Reply::redirect(location);
                reply.status = status;
                Some(reply)
            });
            assert_eq!(
                session(&server).post("/loop", None).unwrap_err().kind,
                ErrorKind::Network,
                "HTTP {status} to {location}"
            );
            assert_eq!(server.count(), 1);
        }
    }
}

#[test]
fn get_transport_failure_is_network_without_retry() {
    let server = Server::start(|_, _| None);
    assert_eq!(
        session(&server).get_page("/page").unwrap_err().kind,
        ErrorKind::Network
    );
    assert_eq!(server.count(), 1);
}

#[test]
fn one_mib_body_limit_allows_boundary_and_rejects_overflow() {
    const LIMIT: usize = 1024 * 1024;
    for html in [false, true] {
        for size in [LIMIT - 1, LIMIT, LIMIT + 1] {
            let server = Server::start(move |_, _| {
                let mut reply = if html {
                    Reply::page(json!({"ok":true}))
                } else {
                    Reply::json(json!({"ok":true}))
                };
                reply.body.push_str(&" ".repeat(size - reply.body.len()));
                Some(reply)
            });
            let client = session(&server);
            let result = if html {
                client.get_page("/page")
            } else {
                client.post("/api", None)
            };
            if size <= LIMIT {
                assert_eq!(result.unwrap(), json!({"ok":true}));
            } else {
                let error = result.unwrap_err();
                assert_eq!(error.kind, ErrorKind::Parsing);
                assert!(error.message.contains("1 MiB"));
            }
            assert_eq!(server.count(), 1);
        }
    }
}

// Byte-level responses are needed for missing Content-Length, chunked bodies,
// truncated transfers and gzip. The shared mock deliberately sends close + CL.
fn raw_server(response: Vec<u8>) -> (String, std::thread::JoinHandle<()>) {
    use std::io::{Read, Write};
    use std::net::TcpListener;

    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let base = format!("http://{}", listener.local_addr().unwrap());
    let worker = std::thread::spawn(move || {
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
        let _ = stream.write_all(&response);
    });
    (base, worker)
}

#[test]
fn streamed_bodies_are_limited_without_trusting_content_length() {
    const LIMIT: usize = 1024 * 1024;
    for chunked in [false, true] {
        for size in [LIMIT, LIMIT + 1] {
            let body = format!("{{}}{}", " ".repeat(size - 2));
            let mut response =
                b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\n"
                    .to_vec();
            if chunked {
                response.extend_from_slice(
                    format!("Transfer-Encoding: chunked\r\n\r\n{size:x}\r\n{body}\r\n0\r\n\r\n")
                        .as_bytes(),
                );
            } else {
                response.extend_from_slice(format!("\r\n{body}").as_bytes());
            }
            let (base, worker) = raw_server(response);
            let client =
                Session::new(&base, Duration::from_secs(2), Duration::from_secs(5)).unwrap();
            let result = client.post("/api", None);
            worker.join().unwrap();
            if size <= LIMIT {
                assert_eq!(result.unwrap(), json!({}));
            } else {
                let error = result.unwrap_err();
                assert_eq!(error.kind, ErrorKind::Parsing);
                assert!(error.message.contains("1 MiB"));
            }
        }
    }
}

#[test]
fn truncated_post_body_is_a_network_error_not_a_parse_error() {
    let (base, worker) = raw_server(
        b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{}".to_vec(),
    );
    let client = Session::new(&base, Duration::from_secs(2), Duration::from_secs(5)).unwrap();
    assert_eq!(
        client.post("/api", None).unwrap_err().kind,
        ErrorKind::Network
    );
    worker.join().unwrap();
}

fn gzip_response(body: &[u8]) -> Vec<u8> {
    let mut response = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Encoding: gzip\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        body.len()
    )
    .into_bytes();
    response.extend_from_slice(body);
    response
}

#[test]
fn gzip_is_decoded_and_invalid_gzip_is_a_network_error() {
    // gzip.compress(b'{"ok":true}', mtime=0), no compression test dependency.
    let fixture = b"\x1f\x8b\x08\x00\x00\x00\x00\x00\x02\xff\xab\x56\xca\xcf\x56\xb2\x2a\x29\x2a\x4d\xad\x05\x00\x90\x5f\xd4\xa7\x0b\x00\x00\x00";
    let (base, worker) = raw_server(gzip_response(fixture));
    let client = Session::new(&base, Duration::from_secs(2), Duration::from_secs(5)).unwrap();
    assert_eq!(client.post("/api", None).unwrap(), json!({"ok":true}));
    worker.join().unwrap();

    for body in [b"invalid gzip".as_slice(), &fixture[..fixture.len() - 4]] {
        let (base, worker) = raw_server(gzip_response(body));
        let client = Session::new(&base, Duration::from_secs(2), Duration::from_secs(5)).unwrap();
        assert_eq!(
            client.post("/api", None).unwrap_err().kind,
            ErrorKind::Network
        );
        worker.join().unwrap();
    }
}

#[test]
fn gzip_body_limit_is_on_decompressed_bytes_not_wire_length() {
    // Deterministic gzip fixtures for b'{"ok":true}' padded with spaces to
    // exactly 1 MiB / 1 MiB + 1. Both compress to only 1067 bytes on the wire.
    for oversized in [false, true] {
        let mut compressed = b"\x1f\x8b\x08\x00\x00\x00\x00\x00\x02\xff\xed\xc1\x31\x0d\x00\x20\x0c\x00\x30\x2b\x64\x32\xf0\xb3\x8b\x83\x84\xc0\xb5\xe0\x1d\x0f\xdc\x6d\x2b\xe6\x88\xbe\xd7\xc9\xdb".to_vec();
        compressed.resize(compressed.len() + 1016, 0);
        compressed.extend_from_slice(if oversized {
            b"\xbe\x3c\x24\xba\x8e\xd8\x01\x00\x10\x00"
        } else {
            b"\x7e\x3c\xae\x8e\xfb\x8c\x00\x00\x10\x00"
        });
        assert_eq!(compressed.len(), 1067);
        let (base, worker) = raw_server(gzip_response(&compressed));
        let client = Session::new(&base, Duration::from_secs(2), Duration::from_secs(5)).unwrap();
        let result = client.post("/api", None);
        worker.join().unwrap();
        if oversized {
            let error = result.unwrap_err();
            assert_eq!(error.kind, ErrorKind::Parsing);
            assert!(error.message.contains("1 MiB"));
        } else {
            assert_eq!(result.unwrap(), json!({"ok":true}));
        }
    }
}

#[test]
fn zero_wait_does_not_bypass_an_expired_deadline() {
    let server = Server::start(|_, _| panic!("No request expected"));
    let client = session(&server);
    client.wait(Duration::ZERO).unwrap();
    let expired = Session::new(&server.base, Duration::from_secs(1), Duration::ZERO).unwrap();
    assert_eq!(
        expired.wait(Duration::ZERO).unwrap_err().kind,
        ErrorKind::Network
    );
    assert_eq!(server.count(), 0);
}
