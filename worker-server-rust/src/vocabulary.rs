//! In-memory answer candidates, in collection order, with reverse lookup.

use std::collections::HashMap;

use unicode_normalization::UnicodeNormalization;

use crate::error::{Error, Result};

#[derive(Debug, Clone, Default)]
pub struct Vocabulary {
    direct: HashMap<String, Vec<String>>,
    reverse: HashMap<String, Vec<String>>,
}

impl Vocabulary {
    pub fn new() -> Self {
        Self::default()
    }

    /// Insert a foreign word and its native-language translation. Native
    /// prompts take precedence over reverse prompts, matching LessonAnswers.
    pub fn insert(&mut self, word: &str, translation: &str) -> Result<()> {
        let word = normalize_whitespace(word);
        let translation = normalize_whitespace(translation);
        if word.is_empty() || translation.is_empty() {
            return Err(Error::parsing(
                "Vocabulary words and translations must not be blank.",
            ));
        }

        insert_candidate(&mut self.direct, &translation, &word);
        insert_candidate(&mut self.reverse, &word, &translation);
        Ok(())
    }

    /// Resolve a normalized, case-insensitive prompt without guessing. Answers
    /// retain their original spelling; duplicates do not consume retry slots.
    pub fn resolve(&self, prompt: &str) -> Option<&[String]> {
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
    value.split_whitespace().collect::<Vec<_>>().join(" ")
}

pub(crate) fn normalized_key(value: &str) -> String {
    // Fold case without expanding a character into several: Maße/Masse and
    // ligatures are different words, not duplicate answer candidates. Applying
    // single-character upper then lower also unifies final sigma and ß/ẞ.
    normalize_whitespace(value)
        .nfc()
        .map(|character| {
            let upper = single_character(character.to_uppercase(), character);
            single_character(upper.to_lowercase(), upper)
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

fn insert_candidate(map: &mut HashMap<String, Vec<String>>, prompt: &str, answer: &str) {
    let candidates = map.entry(normalized_key(prompt)).or_default();
    let answer_key = normalized_key(answer);
    if !candidates
        .iter()
        .any(|candidate| normalized_key(candidate) == answer_key)
    {
        candidates.push(answer.to_owned());
    }
}

#[cfg(test)]
#[path = "../tests/unit/vocabulary.rs"]
mod tests;
