//! In-memory answer candidates, in collection order, with reverse lookup.

use std::collections::{HashMap, HashSet};

use unicode_normalization::UnicodeNormalization;

use crate::error::{Error, Result};

// A vocabulary may span many wordsets. Bound both unique word/translation
// pairs and all retained string bytes, including the deduplication index.
// Hash-table/vector headers and spare capacity are bounded by these limits too.
const MAX_ENTRIES: usize = 10_000;
const MAX_TEXT_BYTES: usize = 1024 * 1024;
const MAX_TERM_BYTES: usize = 16 * 1024;

#[derive(Debug, Clone, Default)]
pub struct Vocabulary {
    direct: HashMap<String, Vec<String>>,
    reverse: HashMap<String, Vec<String>>,
    // One index for both directions avoids re-normalizing or scanning existing
    // candidates, even when every entry has the same ambiguous prompt.
    entries: HashSet<(String, String)>,
    text_bytes: usize,
}

impl Vocabulary {
    pub fn new() -> Self {
        Self::default()
    }

    /// Insert a foreign word and its native-language translation. Native
    /// prompts take precedence over reverse prompts, matching LessonAnswers.
    pub fn insert(&mut self, word: &str, translation: &str) -> Result<()> {
        // Check before allocating or doing Unicode normalization. Padding and
        // pathological combining sequences must also have bounded input cost.
        if word.len() > MAX_TERM_BYTES || translation.len() > MAX_TERM_BYTES {
            return Err(Error::parsing("Vocabulary text exceeds the length limit."));
        }
        let word = normalize_whitespace(word);
        let translation = normalize_whitespace(translation);
        if word.is_empty() || translation.is_empty() {
            return Err(Error::parsing(
                "Vocabulary words and translations must not be blank.",
            ));
        }

        let keys = (fold_case(&word), fold_case(&translation));
        if keys.0.len() > MAX_TERM_BYTES || keys.1.len() > MAX_TERM_BYTES {
            return Err(Error::parsing("Vocabulary key exceeds the length limit."));
        }
        if self.entries.contains(&keys) {
            return Ok(());
        }
        if self.entries.len() >= MAX_ENTRIES {
            return Err(Error::parsing("Vocabulary exceeds the unique entry limit."));
        }

        let mut added_bytes = word.len() + translation.len() + keys.0.len() + keys.1.len();
        if !self.direct.contains_key(&keys.1) {
            added_bytes += keys.1.len();
        }
        if !self.reverse.contains_key(&keys.0) {
            added_bytes += keys.0.len();
        }
        if added_bytes > MAX_TEXT_BYTES - self.text_bytes {
            return Err(Error::parsing("Vocabulary exceeds the total text limit."));
        }

        // All checks precede either map mutation: a rejected entry cannot leave
        // a one-directional answer or consume any of the remaining budget.
        insert_candidate(&mut self.direct, &keys.1, word);
        insert_candidate(&mut self.reverse, &keys.0, translation);
        self.entries.insert(keys);
        self.text_bytes += added_bytes;
        Ok(())
    }

    /// Resolve a normalized, case-insensitive prompt without guessing. Answers
    /// retain their original spelling; duplicates do not consume retry slots.
    pub fn resolve(&self, prompt: &str) -> Option<&[String]> {
        if prompt.len() > MAX_TERM_BYTES {
            return None;
        }
        let key = normalized_key(prompt);
        self.direct
            .get(&key)
            .or_else(|| self.reverse.get(&key))
            .map(Vec::as_slice)
    }

    pub fn is_empty(&self) -> bool {
        self.direct.is_empty()
    }

    /// Number of distinct native-language prompts (not answer candidates).
    pub fn len(&self) -> usize {
        self.direct.len()
    }
}

pub(crate) fn normalize_whitespace(value: &str) -> String {
    let mut normalized = String::new();
    for part in value.split_whitespace() {
        if !normalized.is_empty() {
            normalized.push(' ');
        }
        normalized.push_str(part);
    }
    normalized
}

pub(crate) fn normalized_key(value: &str) -> String {
    fold_case(&normalize_whitespace(value))
}

fn fold_case(value: &str) -> String {
    // Fold case without expanding a character into several: Maße/Masse and
    // ﬃ/ffi must remain different. Single-character upper then lower unifies
    // final sigma and ß/ẞ. Its exceptions to default simple-fold equivalence
    // (Unicode 17) are dotless ı, which must stay distinct from I/i, and the
    // long-s/t ligature, whose simple fold is the ordinary s/t ligature. NFC
    // handles the canonically equivalent Greek dialytika/oxia variants.
    value
        .nfc()
        .map(|character| match character {
            '\u{131}' => character,
            '\u{fb05}' => '\u{fb06}',
            _ => {
                let upper = single_character(character.to_uppercase(), character);
                single_character(upper.to_lowercase(), upper)
            }
        })
        .nfc()
        .collect()
}

fn single_character(mut mapped: impl Iterator<Item = char>, original: char) -> char {
    let first = mapped.next().unwrap_or(original);
    if mapped.next().is_none() {
        first
    } else {
        original
    }
}

fn insert_candidate(map: &mut HashMap<String, Vec<String>>, prompt_key: &str, answer: String) {
    if let Some(candidates) = map.get_mut(prompt_key) {
        candidates.push(answer);
    } else {
        map.insert(prompt_key.to_owned(), vec![answer]);
    }
}

#[cfg(test)]
#[path = "../tests/unit/vocabulary.rs"]
mod tests;
