//! Strict dashboard challenge parsing and the desktop perfectionism matcher.

use std::collections::HashSet;

use serde_json::Value;
use unicode_normalization::{UnicodeNormalization, char::is_combining_mark};

use crate::error::{Error, Result};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ChallengeStatus {
    Available,
    InProgress,
    Completed,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ChallengeInfo {
    pub id: i64,
    pub title: String,
    pub description: String,
    pub points: i32,
    pub status: ChallengeStatus,
}

impl ChallengeInfo {
    pub fn is_available(&self) -> bool {
        self.status == ChallengeStatus::Available
    }

    pub fn is_active(&self) -> bool {
        self.status == ChallengeStatus::InProgress
    }

    pub fn is_perfectionism(&self) -> bool {
        self.is_active() && is_perfectionism(&self.title, &self.description)
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ChallengeSnapshot {
    pub challenges: Vec<ChallengeInfo>,
}

impl ChallengeSnapshot {
    pub fn active(&self) -> Option<&ChallengeInfo> {
        self.challenges
            .iter()
            .find(|challenge| challenge.is_active())
    }

    pub fn active_perfectionism(&self) -> Option<&ChallengeInfo> {
        self.active()
            .filter(|challenge| challenge.is_perfectionism())
    }

    pub fn best_available(&self) -> Option<&ChallengeInfo> {
        // Keep the first dashboard entry on ties, as C#'s stable OrderBy does.
        self.challenges
            .iter()
            .filter(|challenge| challenge.is_available())
            .fold(None, |best, candidate| match best {
                Some(previous) if previous.points >= candidate.points => Some(previous),
                _ => Some(candidate),
            })
    }
}

/// Parse the `dashboard` object, not its enclosing page. Unknown states, duplicate
/// IDs and multiple active challenges are unsafe: none may be silently ignored.
pub fn parse_challenges(dashboard: &Value) -> Result<ChallengeSnapshot> {
    let items = dashboard
        .get("challenges")
        .and_then(Value::as_array)
        .ok_or_else(|| Error::parsing("Missing or invalid dashboard challenges."))?;
    let mut challenges = Vec::with_capacity(items.len());
    let mut ids = HashSet::with_capacity(items.len());
    let mut active_count = 0;

    for item in items {
        let id = item
            .get("id")
            .and_then(Value::as_i64)
            .filter(|id| *id > 0)
            .ok_or_else(|| Error::parsing("Invalid challenge ID."))?;
        if !ids.insert(id) {
            return Err(Error::parsing("Duplicate challenge ID."));
        }
        let title = item
            .get("title")
            .and_then(Value::as_str)
            .map(normalize_text)
            .filter(|title| !title.is_empty())
            .ok_or_else(|| Error::parsing("Missing or invalid challenge title."))?;
        // The HTTP payload requires this field. Like GetString() ?? "" in the
        // desktop parser, an explicit null means an empty description; a missing
        // field or a value of another type is a changed payload, not a default.
        let description = match item.get("description") {
            Some(Value::String(description)) => normalize_text(description),
            Some(Value::Null) => String::new(),
            _ => return Err(Error::parsing("Missing or invalid challenge description.")),
        };
        let points = item
            .get("prize")
            .and_then(Value::as_i64)
            .and_then(|points| i32::try_from(points).ok())
            .ok_or_else(|| Error::parsing("Missing or invalid challenge prize."))?;
        let status = match item.get("status").and_then(Value::as_str) {
            Some("available") => ChallengeStatus::Available,
            Some("in_progress") => {
                active_count += 1;
                if active_count > 1 {
                    return Err(Error::parsing("Multiple active dashboard challenges."));
                }
                ChallengeStatus::InProgress
            }
            Some("completed") => ChallengeStatus::Completed,
            _ => return Err(Error::parsing("Missing or unknown challenge status.")),
        };
        challenges.push(ChallengeInfo {
            id,
            title,
            description,
            points,
            status,
        });
    }

    Ok(ChallengeSnapshot { challenges })
}

/// A successful POST is not proof that the chosen challenge became active.
pub fn confirm_challenge_join(snapshot: &ChallengeSnapshot, challenge_id: i64) -> Result<()> {
    if snapshot
        .active()
        .is_some_and(|active| active.id == challenge_id)
    {
        Ok(())
    } else {
        Err(Error::parsing(
            "HTTP challenge join was not confirmed; stopped before starting the lesson.",
        ))
    }
}

pub fn normalize_text(value: &str) -> String {
    value.split_whitespace().collect::<Vec<_>>().join(" ")
}

pub fn normalize_for_matching(value: &str) -> String {
    let mut normalized = String::with_capacity(value.len());
    let mut separator = false;
    for character in value
        .nfd()
        .filter(|character| !is_combining_mark(*character))
    {
        // Polish ł is not decomposed by Unicode normalization.
        let character = match character {
            'ł' => 'l',
            'Ł' => 'L',
            other => other,
        };
        for lower in character.to_lowercase() {
            if lower.is_alphanumeric() {
                if separator && !normalized.is_empty() {
                    normalized.push(' ');
                }
                normalized.push(lower);
                separator = false;
            } else {
                separator = true;
            }
        }
    }
    normalized
}

/// Port of ChallengeMatcher.IsPerfectionism. Token matching implements its
/// normalized word-boundary regexes without requiring another regex dependency.
pub fn is_perfectionism(title: &str, description: &str) -> bool {
    let title = normalize_for_matching(title);
    let description = normalize_for_matching(description);
    let title_words: Vec<_> = title.split_whitespace().collect();
    let description_words: Vec<_> = description.split_whitespace().collect();
    let keyword_in_title = title_words.iter().any(|word| is_keyword(word));
    let keyword_in_description = description_words.iter().any(|word| is_keyword(word));
    if !keyword_in_title && !keyword_in_description {
        return false;
    }

    let words: Vec<_> = title_words.into_iter().chain(description_words).collect();
    if words
        .windows(2)
        .any(|pair| matches!(pair[0], "nie" | "bez" | "brak") && is_keyword(pair[1]))
    {
        return false;
    }

    let max_errors = find_max_error_limit(&words);
    let no_errors = words
        .windows(2)
        .any(|pair| pair[0] == "bez" && is_error_word(pair[1]));
    if max_errors.is_none() && !no_errors {
        return keyword_in_title;
    }

    let one_or_fewer = no_errors
        || max_errors.is_some_and(|count| match count.parse::<i32>() {
            Ok(number) => number <= 1,
            Err(_) => {
                count.starts_with("jed")
                    || count.starts_with("zer")
                    || matches!(count, "one" | "zero")
            }
        });
    one_or_fewer
        && words
            .iter()
            .any(|word| word.starts_with("lekcj") || word.starts_with("lesson"))
}

fn is_keyword(word: &str) -> bool {
    word.starts_with("perfekcjonizm")
}

fn is_error_word(word: &str) -> bool {
    word.starts_with("blad") || word.starts_with("bled") || word.starts_with("error")
}

fn find_max_error_limit<'a>(words: &[&'a str]) -> Option<&'a str> {
    const PREFIXES: &[&[&str]] = &[
        &["maks"],
        &["maksymalnie"],
        &["max"],
        &["maximum"],
        &["najwyzej"],
        &["nie", "wiecej", "niz"],
        &["do"],
        &["at", "most"],
        &["no", "more", "than"],
    ];
    for start in 0..words.len() {
        for prefix in PREFIXES {
            let remaining = &words[start..];
            if !remaining.starts_with(prefix) || remaining.len() < prefix.len() + 2 {
                continue;
            }
            let count = remaining[prefix.len()];
            let valid_count = count.bytes().all(|byte| byte.is_ascii_digit())
                || count.bytes().all(|byte| byte.is_ascii_lowercase());
            if valid_count && is_error_word(remaining[prefix.len() + 1]) {
                return Some(count);
            }
        }
    }
    None
}

#[cfg(test)]
#[path = "../tests/unit/challenge.rs"]
mod tests;
