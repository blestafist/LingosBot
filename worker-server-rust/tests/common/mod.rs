#![allow(dead_code)]
use serde_json::Value;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};
use std::thread::{self, JoinHandle};
use std::time::Duration;

#[derive(Clone, Debug)]
pub struct Request {
    pub method: String,
    pub path: String,
    pub headers: String,
    pub body: String,
}

pub struct Reply {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: String,
}
impl Reply {
    pub fn html(body: impl Into<String>) -> Self {
        Self {
            status: 200,
            headers: vec![("Content-Type".into(), "text/html; charset=utf-8".into())],
            body: body.into(),
        }
    }
    pub fn page(value: Value) -> Self {
        let raw = value.to_string();
        Self::html(format!(
            "<div id='app' data-props=\"{}\"></div>",
            html_escape::encode_double_quoted_attribute(&raw)
        ))
    }
    pub fn json(value: Value) -> Self {
        Self {
            status: 200,
            headers: vec![("Content-Type".into(), "application/json".into())],
            body: value.to_string(),
        }
    }
    pub fn redirect(destination: &str) -> Self {
        Self {
            status: 302,
            headers: vec![("Location".into(), destination.into())],
            body: String::new(),
        }
    }
    pub fn header(mut self, name: &str, value: &str) -> Self {
        self.headers.push((name.into(), value.into()));
        self
    }
}

pub struct Server {
    pub base: String,
    pub requests: Arc<Mutex<Vec<Request>>>,
    stop: Arc<AtomicBool>,
    thread: Option<JoinHandle<()>>,
}

impl Server {
    /// Real loopback HTTP, including reqwest's cookie jar and redirect handling.
    pub fn start(
        mut handler: impl FnMut(&Request, usize) -> Option<Reply> + Send + 'static,
    ) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        listener.set_nonblocking(true).unwrap();
        let stop = Arc::new(AtomicBool::new(false));
        let requests = Arc::new(Mutex::new(Vec::new()));
        let (stopped, seen) = (Arc::clone(&stop), Arc::clone(&requests));
        let thread = thread::spawn(move || {
            while !stopped.load(Ordering::Relaxed) {
                let (mut stream, _) = match listener.accept() {
                    Ok(pair) => pair,
                    Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                        thread::sleep(Duration::from_millis(2));
                        continue;
                    }
                    Err(error) => panic!("test listener: {error}"),
                };
                let Some(request) = read_request(&mut stream) else {
                    continue;
                };
                let index = {
                    let mut requests = seen.lock().unwrap();
                    requests.push(request.clone());
                    requests.len()
                };
                if let Some(reply) = handler(&request, index) {
                    let mut wire = format!(
                        "HTTP/1.1 {} Test\r\nConnection: close\r\nContent-Length: {}\r\n",
                        reply.status,
                        reply.body.len()
                    );
                    for (name, value) in reply.headers {
                        wire.push_str(&format!("{name}: {value}\r\n"));
                    }
                    wire.push_str("\r\n");
                    wire.push_str(&reply.body);
                    let _ = stream.write_all(wire.as_bytes());
                }
            }
        });
        Self {
            base,
            requests,
            stop,
            thread: Some(thread),
        }
    }
    pub fn count(&self) -> usize {
        self.requests.lock().unwrap().len()
    }
}
impl Drop for Server {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Relaxed);
        if let Some(thread) = self.thread.take() {
            let result = thread.join();
            if !std::thread::panicking() {
                result.unwrap();
            }
        }
    }
}

fn read_request(stream: &mut TcpStream) -> Option<Request> {
    stream.set_read_timeout(Some(Duration::from_secs(2))).ok()?;
    let mut bytes = Vec::new();
    let header_end = loop {
        let mut buf = [0u8; 4096];
        let n = stream.read(&mut buf).ok()?;
        if n == 0 {
            return None;
        }
        bytes.extend_from_slice(&buf[..n]);
        if let Some(pos) = bytes.windows(4).position(|w| w == b"\r\n\r\n") {
            break pos + 4;
        }
        if bytes.len() > 65536 {
            return None;
        }
    };
    let headers = String::from_utf8(bytes[..header_end].to_vec()).ok()?;
    let length = headers
        .lines()
        .find_map(|line| {
            let (name, value) = line.split_once(':')?;
            name.eq_ignore_ascii_case("content-length")
                .then(|| value.trim().parse::<usize>().ok())
                .flatten()
        })
        .unwrap_or(0);
    while bytes.len() < header_end + length {
        let mut buf = [0u8; 4096];
        let n = stream.read(&mut buf).ok()?;
        if n == 0 {
            return None;
        }
        bytes.extend_from_slice(&buf[..n]);
    }
    let mut line = headers.lines().next()?.split_whitespace();
    Some(Request {
        method: line.next()?.into(),
        path: line.next()?.into(),
        body: String::from_utf8(bytes[header_end..header_end + length].to_vec()).ok()?,
        headers,
    })
}
