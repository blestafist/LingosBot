use std::fmt;

pub type Result<T> = std::result::Result<T, Error>;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ErrorKind {
    Auth,
    Network,
    Parsing,
    Config,
    Limit,
}

/// Only locally authored, sanitized messages belong here. Never wrap HTTP bodies,
/// URLs, parser errors or credentials: this error is part of the public protocol.
#[derive(Debug)]
pub struct Error {
    pub kind: ErrorKind,
    pub message: String,
}

impl Error {
    pub fn auth(message: impl Into<String>) -> Self {
        Self {
            kind: ErrorKind::Auth,
            message: message.into(),
        }
    }
    pub fn network(message: impl Into<String>) -> Self {
        Self {
            kind: ErrorKind::Network,
            message: message.into(),
        }
    }
    pub fn parsing(message: impl Into<String>) -> Self {
        Self {
            kind: ErrorKind::Parsing,
            message: message.into(),
        }
    }
    pub fn config(message: impl Into<String>) -> Self {
        Self {
            kind: ErrorKind::Config,
            message: message.into(),
        }
    }
    pub fn limit(message: impl Into<String>) -> Self {
        Self {
            kind: ErrorKind::Limit,
            message: message.into(),
        }
    }
    pub fn is_limit(&self) -> bool {
        self.kind == ErrorKind::Limit
    }
    pub fn code(&self) -> &'static str {
        match self.kind {
            ErrorKind::Auth => "AUTH_FAILED",
            ErrorKind::Network => "NETWORK_ERROR",
            ErrorKind::Parsing => "PARSING_ERROR",
            ErrorKind::Config => "CONFIG_ERROR",
            ErrorKind::Limit => "LESSON_LIMIT_REACHED",
        }
    }
    pub fn exit_code(&self) -> u8 {
        match self.kind {
            ErrorKind::Auth => 1,
            ErrorKind::Network => 2,
            ErrorKind::Parsing | ErrorKind::Config | ErrorKind::Limit => 3,
        }
    }
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}
impl std::error::Error for Error {}
