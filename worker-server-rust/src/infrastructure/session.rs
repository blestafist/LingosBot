use super::html;
use crate::error::{Error, Result};
use crate::lesson::LessonClient;
use crate::models::{AnswerRequest, AnswerResult, LearningProps};
use reqwest::blocking::{Client, RequestBuilder, Response};
use reqwest::header::{ACCEPT, CONTENT_TYPE, LOCATION};
use reqwest::{StatusCode, Url};
use serde_json::Value;
use std::io::Read;
use std::time::{Duration, Instant};

const MAX_BODY_BYTES: u64 = 8 * 1024 * 1024;
const MAX_REDIRECTS: usize = 5;

/// One client, cookie jar and connection pool per process. Nothing is persisted.
pub struct Session {
    client: Client,
    origin: Url,
    timeout: Duration,
    deadline: Instant,
}

impl LessonClient for Session {
    fn navigate(&mut self, url: &str) -> Result<LearningProps> {
        serde_json::from_value(self.get_page(url)?)
            .map_err(|_| Error::parsing("Invalid learning page data; stopped safely."))
    }

    fn answer(&mut self, lesson_id: u64, request: &AnswerRequest) -> Result<AnswerResult> {
        let body = serde_json::to_value(request)
            .map_err(|_| Error::parsing("Could not encode the answer request."))?;
        serde_json::from_value(self.post(&format!("/learning/answer/{lesson_id}"), Some(&body))?)
            .map_err(|_| Error::parsing("Invalid answer response; request was not replayed."))
    }

    fn wait(&mut self, duration: Duration) -> Result<()> {
        Session::wait(self, duration)
    }
}

impl Session {
    pub fn new(base_url: &str, timeout: Duration, max_run: Duration) -> Result<Self> {
        let origin = Url::parse(base_url).map_err(|_| Error::config("Invalid base URL."))?;
        if !origin.username().is_empty()
            || origin.password().is_some()
            || origin.host_str().is_none()
            || !matches!(origin.scheme(), "http" | "https")
        {
            return Err(Error::config("Invalid HTTP origin."));
        }
        let client = Client::builder()
            .cookie_store(true)
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .user_agent(concat!(
                "LingosBot/",
                env!("CARGO_PKG_VERSION"),
                " (HTTP worker)"
            ))
            .connect_timeout(timeout.min(Duration::from_secs(10)))
            .timeout(timeout)
            .pool_max_idle_per_host(1)
            .pool_idle_timeout(Duration::from_secs(30))
            .no_proxy()
            .build()
            .map_err(|_| Error::network("Could not initialize the HTTP/TLS client."))?;
        let deadline = Instant::now()
            .checked_add(max_run)
            .ok_or_else(|| Error::config("Invalid run deadline."))?;
        Ok(Self {
            client,
            origin,
            timeout,
            deadline,
        })
    }

    pub fn same_origin(&self, url: &str) -> Result<Url> {
        self.resolve_from(&self.origin, url)
    }

    fn resolve_from(&self, base: &Url, url: &str) -> Result<Url> {
        let target = base
            .join(url)
            .map_err(|_| Error::parsing("Invalid HTTP destination."))?;
        if target.origin() != self.origin.origin()
            || !target.username().is_empty()
            || target.password().is_some()
        {
            return Err(Error::parsing(
                "Refusing HTTP navigation outside the configured origin.",
            ));
        }
        Ok(target)
    }

    fn remaining(&self) -> Result<Duration> {
        self.deadline
            .checked_duration_since(Instant::now())
            .filter(|time| !time.is_zero())
            .ok_or_else(|| Error::network("Run deadline exceeded; no request was replayed."))
    }

    pub fn wait(&self, duration: Duration) -> Result<()> {
        if duration >= self.remaining()? {
            return Err(Error::network(
                "Run deadline would be exceeded by the answer delay.",
            ));
        }
        std::thread::sleep(duration);
        Ok(())
    }

    fn send(&self, request: RequestBuilder, is_post: bool) -> Result<Response> {
        request
            .timeout(self.timeout.min(self.remaining()?))
            .send()
            .map_err(|error| {
                // reqwest errors can embed complete URLs. Do not expose them.
                let reason = if error.is_timeout() {
                    "HTTP request timed out"
                } else if error.is_connect() {
                    "HTTP connection failed"
                } else {
                    "HTTP request failed"
                };
                Error::network(if is_post {
                    format!("{reason}; the server may have processed the request. Not retried.")
                } else {
                    format!("{reason}; not retried.")
                })
            })
    }

    fn require_success(response: &Response) -> Result<()> {
        match response.status() {
            StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN => Err(Error::auth(
                "Authentication was rejected or the session expired.",
            )),
            status if !status.is_success() => Err(Error::network(format!(
                "HTTP {}; stopped without retrying.",
                status.as_u16()
            ))),
            _ => Ok(()),
        }
    }

    fn body(response: Response, expected: &str) -> Result<String> {
        let content_type = response
            .headers()
            .get(CONTENT_TYPE)
            .and_then(|header| header.to_str().ok())
            .and_then(|header| header.split(';').next())
            .unwrap_or("")
            .trim();
        if !content_type.eq_ignore_ascii_case(expected) {
            return Err(Error::parsing(
                "Unexpected HTTP content type; stopped without retrying.",
            ));
        }
        if response
            .content_length()
            .is_some_and(|size| size > MAX_BODY_BYTES)
        {
            return Err(Error::parsing(
                "HTTP response exceeds the 8 MiB safety limit.",
            ));
        }
        let mut bytes = Vec::new();
        response
            .take(MAX_BODY_BYTES + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| Error::network("Could not read HTTP response; request not replayed."))?;
        if bytes.len() as u64 > MAX_BODY_BYTES {
            return Err(Error::parsing(
                "HTTP response exceeds the 8 MiB safety limit.",
            ));
        }
        String::from_utf8(bytes).map_err(|_| Error::parsing("HTTP response is not valid UTF-8."))
    }

    fn login_path(url: &Url) -> bool {
        url.path()
            .trim_end_matches('/')
            .eq_ignore_ascii_case("/h/login")
    }

    fn get_html(&self, url: &str, allow_login: bool) -> Result<String> {
        let mut target = self.same_origin(url)?;
        for redirects in 0..=MAX_REDIRECTS {
            if !allow_login && Self::login_path(&target) {
                return Err(Error::auth(
                    "Session expired: redirected to the login page.",
                ));
            }
            let response = self.send(
                self.client.get(target.clone()).header(ACCEPT, "text/html"),
                false,
            )?;
            if response.status().is_redirection() {
                if redirects == MAX_REDIRECTS {
                    return Err(Error::parsing("Too many HTTP redirects; stopped safely."));
                }
                let location = response
                    .headers()
                    .get(LOCATION)
                    .and_then(|v| v.to_str().ok())
                    .ok_or_else(|| Error::parsing("HTTP redirect has no valid destination."))?;
                target = self.resolve_from(&target, location)?;
                continue;
            }
            Self::require_success(&response)?;
            return Self::body(response, "text/html");
        }
        unreachable!()
    }

    pub fn get_page(&self, url: &str) -> Result<Value> {
        let html = self.get_html(url, false)?;
        html::page_props(&html)
    }

    pub fn post(&self, url: &str, body: Option<&Value>) -> Result<Value> {
        let target = self.same_origin(url)?;
        let mut request = self.client.post(target).header(ACCEPT, "application/json");
        if let Some(body) = body {
            request = request.json(body);
        }
        let response = self.send(request, true)?;
        if response.status().is_redirection() {
            if let Some(location) = response
                .headers()
                .get(LOCATION)
                .and_then(|v| v.to_str().ok())
                && Self::login_path(&self.same_origin(location)?)
            {
                return Err(Error::auth("Session expired; POST was not replayed."));
            }
            return Err(Error::network(
                "Unexpected POST redirect; request not followed or replayed.",
            ));
        }
        Self::require_success(&response)?;
        let text = Self::body(response, "application/json")?;
        let value: Value = serde_json::from_str(&text)
            .map_err(|_| Error::parsing("Invalid API JSON; request was not replayed."))?;
        if !value.is_object() {
            return Err(Error::parsing(
                "API returned a non-object response; request was not replayed.",
            ));
        }
        Ok(value)
    }

    /// GET CSRF -> POST form -> verify dashboard with the same in-memory cookies.
    pub fn login(&self, username: &str, password: &str, dashboard_url: &str) -> Result<Value> {
        let dashboard_url = self.same_origin(dashboard_url)?;
        let login_url = self.same_origin("/h/login")?;
        let token = html::csrf_token(&self.get_html(login_url.as_str(), true)?)?;
        let response = self.send(
            self.client
                .post(login_url.clone())
                .header(ACCEPT, "text/html")
                .form(&[
                    ("_username", username),
                    ("_password", password),
                    ("_csrf_token", &token),
                ]),
            true,
        )?;
        if response.status().is_server_error() || response.status() == StatusCode::TOO_MANY_REQUESTS
        {
            Self::require_success(&response)?;
        }
        if !matches!(response.status().as_u16(), 301 | 302 | 303 | 307 | 308) {
            return Err(Error::auth("Nieprawidłowy login lub hasło"));
        }
        let location = response
            .headers()
            .get(LOCATION)
            .and_then(|v| v.to_str().ok())
            .ok_or_else(|| Error::parsing("Login redirect has no destination."))?;
        let target = self.resolve_from(&login_url, location)?;
        if Self::login_path(&target) {
            return Err(Error::auth("Nieprawidłowy login lub hasło"));
        }
        let dashboard = self.get_page(dashboard_url.as_str())?;
        if dashboard.get("page").and_then(Value::as_str) != Some("dashboard") {
            return Err(Error::auth("Authenticated student dashboard did not load."));
        }
        Ok(dashboard)
    }
}
