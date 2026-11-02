//! Explicit configuration loading. This module never creates configuration files.

use std::collections::{BTreeMap, HashSet};
use std::fmt;
#[cfg(unix)]
use std::fs::OpenOptions;
#[cfg(unix)]
use std::io::Read;
use std::net::IpAddr;
use std::path::{Path, PathBuf};

use clap::Parser;
use reqwest::Url;
use serde::Deserialize;

use crate::error::{Error, Result};

const DEFAULT_BASE_URL: &str = "https://lingos.pl";
const DEFAULT_DASHBOARD_PATH: &str = "/student/dashboard";
const MAX_CONFIG_BYTES: u64 = 1024 * 1024;
pub(crate) const MAX_LESSONS: u32 = 10_000;
const MAX_PROMPT_SAFETY_CAP: usize = 10_000;
const MAX_DELAY_MS: u64 = 60_000;

/// CLI values override nonempty environment credentials, then JSON values.
#[derive(Parser, Default)]
#[command(
    name = "lingosbot",
    version,
    about = "Run Lingos lessons using the HTTP API"
)]
pub struct Cli {
    /// Login name; falls back to LINGOS_USERNAME.
    #[arg(short = 'u', long)]
    pub username: Option<String>,

    /// Password; prefer LINGOS_PASSWORD to avoid exposing it in process arguments.
    #[arg(short = 'p', long, allow_hyphen_values = true)]
    pub password: Option<String>,

    /// Read this JSON file. No file is read or created unless explicitly requested.
    #[arg(long, value_name = "PATH")]
    pub config: Option<PathBuf>,

    /// Emit machine-readable output.
    #[arg(long)]
    pub json: bool,

    /// Default lesson count per class (1..=10000).
    #[arg(long)]
    pub lessons: Option<u32>,

    /// Minimum answer delay in milliseconds (0..=60000).
    #[arg(long)]
    pub delay_min_ms: Option<u64>,

    /// Maximum answer delay in milliseconds (0..=60000).
    #[arg(long)]
    pub delay_max_ms: Option<u64>,

    /// Request timeout in seconds (1..=300).
    #[arg(long)]
    pub timeout_seconds: Option<u64>,

    /// Maximum run duration in seconds (1..=86400).
    #[arg(long)]
    pub max_run_seconds: Option<u64>,
}

// Do not derive Debug for anything holding credentials. Even malformed, unresolved
// input must be safe to log; omit all user-provided strings and paths here.
impl fmt::Debug for Cli {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Cli")
            .field("credentials", &"<redacted>")
            .field("json", &self.json)
            .field("lessons", &self.lessons)
            .field("delay_min_ms", &self.delay_min_ms)
            .field("delay_max_ms", &self.delay_max_ms)
            .field("timeout_seconds", &self.timeout_seconds)
            .field("max_run_seconds", &self.max_run_seconds)
            .finish_non_exhaustive()
    }
}

/// Resolved settings. Defaults deliberately contain no credentials.
#[derive(Clone)]
pub struct Config {
    pub username: String,
    pub password: String,
    pub base_url: String,
    pub student_dashboard_url: String,
    pub lesson_count: u32,
    pub class_lesson_counts: BTreeMap<String, u32>,
    pub lesson_prompt_safety_cap: usize,
    pub errors_per100_words: u8,
    pub answer_delay_min_ms: u64,
    pub answer_delay_max_ms: u64,
    pub request_timeout_seconds: u64,
    pub max_run_seconds: u64,
}

impl fmt::Debug for Config {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Config")
            .field("credentials", &"<redacted>")
            .field("lesson_count", &self.lesson_count)
            .field("lesson_prompt_safety_cap", &self.lesson_prompt_safety_cap)
            .field("errors_per100_words", &self.errors_per100_words)
            .field("answer_delay_min_ms", &self.answer_delay_min_ms)
            .field("answer_delay_max_ms", &self.answer_delay_max_ms)
            .field("request_timeout_seconds", &self.request_timeout_seconds)
            .field("max_run_seconds", &self.max_run_seconds)
            .finish_non_exhaustive()
    }
}

impl Default for Config {
    fn default() -> Self {
        Self {
            username: String::new(),
            password: String::new(),
            base_url: DEFAULT_BASE_URL.to_owned(),
            student_dashboard_url: format!("{DEFAULT_BASE_URL}{DEFAULT_DASHBOARD_PATH}"),
            lesson_count: 1,
            class_lesson_counts: BTreeMap::new(),
            lesson_prompt_safety_cap: 30,
            errors_per100_words: 0,
            answer_delay_min_ms: 0,
            answer_delay_max_ms: 0,
            request_timeout_seconds: 30,
            max_run_seconds: 900,
        }
    }
}

// Unknown desktop/browser settings are intentionally ignored. API mode is checked
// separately so a browser configuration cannot silently select a different mode.
#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct FileConfig {
    username: Option<String>,
    password: Option<String>,
    credentials: Option<JsonObject<FileCredentials>>,
    #[serde(alias = "base_url")]
    base_url: Option<String>,
    #[serde(alias = "student_dashboard_url")]
    student_dashboard_url: Option<String>,
    mode: Option<String>,
    #[serde(alias = "lesson_count")]
    lesson_count: Option<u32>,
    #[serde(alias = "class_lesson_counts")]
    class_lesson_counts: Option<BTreeMap<String, u32>>,
    #[serde(alias = "lesson_prompt_safety_cap")]
    lesson_prompt_safety_cap: Option<usize>,
    #[serde(alias = "errors_per100_words")]
    errors_per100_words: Option<u8>,
    #[serde(alias = "answer_delay_min_ms")]
    answer_delay_min_ms: Option<u64>,
    #[serde(alias = "answer_delay_max_ms")]
    answer_delay_max_ms: Option<u64>,
    #[serde(alias = "request_timeout_seconds")]
    request_timeout_seconds: Option<u64>,
    #[serde(alias = "max_run_seconds")]
    max_run_seconds: Option<u64>,
}

#[derive(Default, Deserialize)]
struct FileCredentials {
    email: Option<String>,
    password: Option<String>,
}

// Derived struct deserializers also accept positional arrays. Configuration and
// credentials must be JSON objects, without losing duplicate-field validation.
struct JsonObject<T>(T);

impl<'de, T: Deserialize<'de>> Deserialize<'de> for JsonObject<T> {
    fn deserialize<D: serde::Deserializer<'de>>(
        deserializer: D,
    ) -> std::result::Result<Self, D::Error> {
        struct ObjectVisitor<T>(std::marker::PhantomData<T>);

        impl<'de, T: Deserialize<'de>> serde::de::Visitor<'de> for ObjectVisitor<T> {
            type Value = JsonObject<T>;

            fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str("a configuration object")
            }

            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                map: M,
            ) -> std::result::Result<Self::Value, M::Error> {
                T::deserialize(serde::de::value::MapAccessDeserializer::new(map)).map(JsonObject)
            }
        }

        deserializer.deserialize_map(ObjectVisitor(std::marker::PhantomData))
    }
}

impl Cli {
    /// Resolve CLI > nonempty env > top-level JSON > desktop credentials > defaults.
    /// Only the explicitly supplied config file is read. Passwords are not trimmed.
    pub fn resolve(self) -> Result<Config> {
        let file = match self.config.as_deref() {
            Some(path) => read_config(path)?,
            None => FileConfig::default(),
        };
        if let Some(mode) = file.mode.as_deref() {
            let mode = mode.trim();
            if !mode.is_empty() && !mode.eq_ignore_ascii_case("api") {
                return Err(Error::config("mode must be null, blank, or api"));
            }
        }

        let defaults = Config::default();
        let credentials = file.credentials.map(|object| object.0).unwrap_or_default();
        let base = parse_origin(file.base_url.as_deref().unwrap_or(DEFAULT_BASE_URL))?;
        let dashboard = parse_dashboard(
            &base,
            file.student_dashboard_url
                .as_deref()
                .unwrap_or(DEFAULT_DASHBOARD_PATH),
        )?;
        let answer_delay_min_ms = self
            .delay_min_ms
            .or(file.answer_delay_min_ms)
            .unwrap_or(defaults.answer_delay_min_ms);
        let answer_delay_max_ms = self
            .delay_max_ms
            .or(file.answer_delay_max_ms)
            .unwrap_or(defaults.answer_delay_max_ms.max(answer_delay_min_ms));

        let config = Config {
            username: credential(self.username, "LINGOS_USERNAME")?
                .or(file.username)
                .or(credentials.email)
                .unwrap_or_default()
                .trim()
                .to_owned(),
            password: credential(self.password, "LINGOS_PASSWORD")?
                .or(file.password)
                .or(credentials.password)
                .unwrap_or_default(),
            base_url: base.origin().ascii_serialization(),
            student_dashboard_url: dashboard.to_string(),
            lesson_count: self
                .lessons
                .or(file.lesson_count)
                .unwrap_or(defaults.lesson_count),
            class_lesson_counts: file.class_lesson_counts.unwrap_or_default(),
            lesson_prompt_safety_cap: file
                .lesson_prompt_safety_cap
                .unwrap_or(defaults.lesson_prompt_safety_cap),
            errors_per100_words: file
                .errors_per100_words
                .unwrap_or(defaults.errors_per100_words),
            answer_delay_min_ms,
            answer_delay_max_ms,
            request_timeout_seconds: self
                .timeout_seconds
                .or(file.request_timeout_seconds)
                .unwrap_or(defaults.request_timeout_seconds),
            max_run_seconds: self
                .max_run_seconds
                .or(file.max_run_seconds)
                .unwrap_or(defaults.max_run_seconds),
        };
        config.validate()?;
        Ok(config)
    }
}

impl Config {
    /// Validate settings, including configurations constructed directly by callers.
    /// A zero class override disables that class; the default count must be positive.
    /// Explicit class counts have an aggregate 10000-lesson budget. The runner must
    /// also cap its actual total after discovering classes that use the default.
    pub fn validate(&self) -> Result<()> {
        if self.username.trim().is_empty() {
            return Err(Error::config(
                "username is required; use --username, LINGOS_USERNAME, or config credentials",
            ));
        }
        if self.password.trim().is_empty() {
            return Err(Error::config(
                "password is required; use --password, LINGOS_PASSWORD, or config credentials",
            ));
        }

        let base = parse_origin(&self.base_url)?;
        parse_dashboard(&base, &self.student_dashboard_url)?;

        if !(1..=MAX_LESSONS).contains(&self.lesson_count) {
            return Err(Error::config("lessonCount must be between 1 and 10000"));
        }
        if self.class_lesson_counts.len() > MAX_LESSONS as usize {
            return Err(Error::config(
                "classLessonCounts may have at most 10000 entries",
            ));
        }
        let mut total = 0_u32;
        let mut keys = HashSet::new();
        for (name, &count) in &self.class_lesson_counts {
            if name.trim().is_empty() || count > MAX_LESSONS {
                return Err(Error::config(
                    "classLessonCounts requires nonblank keys and counts between 0 and 10000",
                ));
            }
            if !keys.insert(name.to_lowercase()) {
                return Err(Error::config(
                    "classLessonCounts keys must be unique ignoring case",
                ));
            }
            total = total
                .checked_add(count)
                .filter(|&sum| sum <= MAX_LESSONS)
                .ok_or_else(|| Error::config("classLessonCounts total must not exceed 10000"))?;
        }
        if !(1..=MAX_PROMPT_SAFETY_CAP).contains(&self.lesson_prompt_safety_cap) {
            return Err(Error::config(
                "lessonPromptSafetyCap must be between 1 and 10000",
            ));
        }
        if self.errors_per100_words > 100 {
            return Err(Error::config("errorsPer100Words must be between 0 and 100"));
        }
        if self.answer_delay_min_ms > self.answer_delay_max_ms
            || self.answer_delay_max_ms > MAX_DELAY_MS
        {
            return Err(Error::config(
                "answer delays must satisfy 0 <= answerDelayMinMs <= answerDelayMaxMs <= 60000",
            ));
        }
        if !(1..=300).contains(&self.request_timeout_seconds) {
            return Err(Error::config(
                "requestTimeoutSeconds must be between 1 and 300",
            ));
        }
        if !(1..=86_400).contains(&self.max_run_seconds) {
            return Err(Error::config("maxRunSeconds must be between 1 and 86400"));
        }
        Ok(())
    }
}

fn credential(cli: Option<String>, name: &str) -> Result<Option<String>> {
    if cli.is_some() {
        // An explicit empty CLI value must fail validation, not fall back.
        return Ok(cli);
    }
    std::env::var_os(name)
        .filter(|value| !value.is_empty())
        .map(|value| {
            value.into_string().map_err(|_| {
                Error::config("credential environment variable must contain valid Unicode")
            })
        })
        .transpose()
}

// Config files are supported only on Unix, matching the Linux-only distribution.
// Do not open arbitrary device/pipe paths on platforms without O_NONBLOCK.
#[cfg(not(unix))]
fn read_config(_path: &Path) -> Result<FileConfig> {
    Err(Error::config("config files are supported only on Unix"))
}

#[cfg(unix)]
fn read_config(path: &Path) -> Result<FileConfig> {
    use std::os::unix::fs::OpenOptionsExt;

    let mut options = OpenOptions::new();
    options.read(true);
    // A FIFO must not block before we can reject it. Inspect the opened file
    // rather than the path, so replacing a symlink cannot bypass this check.
    options.custom_flags(libc::O_NONBLOCK);
    let file = options
        .open(path)
        .map_err(|_| Error::config("could not open config file"))?;
    let metadata = file
        .metadata()
        .map_err(|_| Error::config("could not inspect config file"))?;
    if !metadata.is_file() {
        return Err(Error::config("config must be a regular file"));
    }
    if metadata.len() > MAX_CONFIG_BYTES {
        return Err(Error::config("config file must not exceed 1 MiB"));
    }
    // Bound the read as well: the file may grow after checking metadata.
    let mut bytes = Vec::new();
    file.take(MAX_CONFIG_BYTES + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| Error::config("could not read config file"))?;
    if bytes.len() as u64 > MAX_CONFIG_BYTES {
        return Err(Error::config("config file must not exceed 1 MiB"));
    }
    // Serde errors can contain secret string values, including numeric-field input.
    serde_json::from_slice::<JsonObject<FileConfig>>(&bytes)
        .map(|object| object.0)
        .map_err(|_| Error::config("config file must contain a valid JSON configuration object"))
}

pub(crate) fn parse_origin(value: &str) -> Result<Url> {
    let invalid = || {
        Error::config(
            "baseUrl must be an HTTPS origin (HTTP is allowed only on loopback), without credentials, path, query, or fragment",
        )
    };
    let value = value.trim();
    if value.chars().any(char::is_control) || value.contains('\\') {
        return Err(invalid());
    }
    // Inspect the original authority and path too: URL normalization can erase an
    // empty userinfo component or turn a non-root path such as /a/.. into /.
    let (_, rest) = value.split_once("://").ok_or_else(invalid)?;
    let authority = rest.split(['/', '?', '#']).next().unwrap_or_default();
    if authority.is_empty()
        || authority.contains('@')
        || !matches!(&rest[authority.len()..], "" | "/")
    {
        return Err(invalid());
    }
    let url = Url::parse(value).map_err(|_| invalid())?;
    if url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(invalid());
    }
    match url.scheme() {
        "https" => Ok(url),
        "http" if is_loopback_host(&url) => Ok(url),
        _ => Err(invalid()),
    }
}

fn is_loopback_host(url: &Url) -> bool {
    let Some(host) = url.host_str() else {
        return false;
    };
    host.eq_ignore_ascii_case("localhost")
        || host
            .trim_start_matches('[')
            .trim_end_matches(']')
            .parse::<IpAddr>()
            .is_ok_and(|ip| ip.is_loopback())
}

fn parse_dashboard(base: &Url, value: &str) -> Result<Url> {
    let invalid = || {
        Error::config(
            "studentDashboardUrl must be a URL or path on the baseUrl origin, without credentials, query, or fragment",
        )
    };
    let value = value.trim();
    if value.is_empty() || value.chars().any(char::is_control) || value.contains('\\') {
        return Err(invalid());
    }
    let authority = value
        .split_once("://")
        .map(|(_, rest)| rest)
        .or_else(|| value.strip_prefix("//"))
        .map(|rest| rest.split(['/', '?', '#']).next().unwrap_or_default());
    if authority.is_some_and(|authority| authority.contains('@')) {
        return Err(invalid());
    }
    let url = base.join(value).map_err(|_| invalid())?;
    if url.origin() != base.origin()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(invalid());
    }
    Ok(url)
}

#[cfg(test)]
#[path = "../tests/unit/config.rs"]
mod tests;
