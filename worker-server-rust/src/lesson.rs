//! Sequential learning state machine. Only a confirmed rejection permits trying
//! another candidate; a failed or ambiguous POST is always propagated as-is.

use std::{
    collections::{HashMap, HashSet},
    time::Duration,
};

use crate::{
    config::Config,
    error::{Error, Result},
    models::{AnswerRequest, AnswerResult, LearningProps, LearningStep},
    vocabulary::{Vocabulary, normalize_whitespace, normalized_key},
};

pub trait LessonClient {
    fn navigate(&mut self, url: &str) -> Result<LearningProps>;
    fn answer(&mut self, lesson_id: u64, request: &AnswerRequest) -> Result<AnswerResult>;

    /// Enforce session deadlines when waiting, or avoid sleeping in tests.
    fn wait(&mut self, duration: Duration) -> Result<()>;

    /// Called only after the server has provided answer feedback.
    fn progress(
        &mut self,
        _prompt: &str,
        _correct: bool,
        _intentional: bool,
        _words_learned: u64,
    ) -> Result<()> {
        Ok(())
    }
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct LessonStats {
    /// Distinct flashcard exercises with a confirmed, non-intentional success.
    pub words_learned: u64,
    pub answers_submitted: u64,
    /// Server-confirmed rejected flashcard answers (including intentional errors).
    pub errors_made: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct ExerciseFingerprint {
    lesson_id: u64,
    identifier: u64,
    kind: String,
    main: String,
}

#[derive(Default)]
struct ExerciseProgress {
    candidate_index: usize,
    error_considered: bool,
}

pub fn run_lesson(
    client: &mut impl LessonClient,
    config: &Config,
    vocabulary: &Vocabulary,
    perfectionism: bool,
    initial: LearningProps,
) -> Result<LessonStats> {
    let (mut lesson_id, mut step) = validate(initial)?;
    validate_config(config)?;

    let cap = config.lesson_prompt_safety_cap;
    let submission_cap = u64::try_from(cap).unwrap_or(u64::MAX).saturating_mul(4);
    let mut stats = LessonStats::default();
    let mut acknowledged = HashSet::new();
    let mut exercise_progress = HashMap::<ExerciseFingerprint, ExerciseProgress>::new();
    let mut prompts = 0usize;
    let mut transitions = 0usize;
    let mut current_exercise = None;

    loop {
        match step.state.as_str() {
            "finished" => return Ok(stats),
            "change_wordset" => {
                if transitions >= cap {
                    return Err(Error::parsing(
                        "Lesson exceeded the wordset transition safety cap.",
                    ));
                }
                let url = step
                    .continue_url
                    .as_deref()
                    .filter(|url| !url.trim().is_empty())
                    .ok_or_else(|| {
                        Error::parsing("Lesson wordset transition has no continue URL.")
                    })?;
                transitions += 1;
                // Navigation supplies both a fresh step and its lesson ID.
                (lesson_id, step) = validate(client.navigate(url)?)?;
                current_exercise = None;
                acknowledged.clear();
                exercise_progress.clear();
                continue;
            }
            "select_level" => {
                return Err(Error::parsing(
                    "Choose your language level on lingos.pl before running lessons.",
                ));
            }
            "exercise" => {}
            _ => {
                return Err(Error::parsing(
                    "Unrecognized lesson API step. Stopped safely.",
                ));
            }
        }

        let exercise = step
            .exercise
            .as_ref()
            .filter(|exercise| exercise.identifier > 0)
            .ok_or_else(|| Error::parsing("Lesson API exercise has no valid identifier."))?;
        if stats.answers_submitted >= submission_cap {
            return Err(Error::parsing(
                "Lesson exceeded the API submission safety cap.",
            ));
        }

        let prompt = normalize_whitespace(&exercise.main);
        let fingerprint = ExerciseFingerprint {
            lesson_id,
            identifier: exercise.identifier,
            kind: exercise.kind.clone(),
            main: prompt.clone(),
        };
        if acknowledged.contains(&fingerprint) {
            return Err(Error::parsing(
                "Lesson API repeated an already acknowledged exercise. Stopped without resubmitting.",
            ));
        }
        if current_exercise.as_ref() != Some(&fingerprint) {
            current_exercise = Some(fingerprint.clone());
            if exercise.kind == "flashcard" {
                if prompts >= cap {
                    return Err(Error::parsing("Lesson exceeded the prompt safety cap."));
                }
                prompts += 1;
            }
        }

        let progress = exercise_progress.entry(fingerprint.clone()).or_default();
        let is_flashcard = exercise.kind == "flashcard";
        let mut intentional_error = false;
        let answer = match exercise.kind.as_str() {
            "flashcard" => {
                if prompt.is_empty() {
                    return Err(Error::parsing("The lesson API prompt was empty."));
                }
                let candidates = vocabulary.resolve(&prompt).ok_or_else(|| {
                    Error::parsing(
                        "No collected translation exists for this prompt. Stopped without guessing.",
                    )
                })?;
                let candidate = candidates.get(progress.candidate_index).ok_or_else(|| {
                    Error::parsing(format!(
                        "All {} stored answers were rejected for this prompt.",
                        candidates.len()
                    ))
                })?;
                if !progress.error_considered {
                    progress.error_considered = true;
                    intentional_error = make_error(config.errors_per100_words, perfectionism);
                }
                if intentional_error {
                    wrong_answer(candidate, candidates)
                } else {
                    candidate.clone()
                }
            }
            "new_teacher" | "new_premium" => String::new(),
            _ => {
                return Err(Error::parsing(
                    "Unrecognized lesson API exercise. Stopped without guessing.",
                ));
            }
        };

        let request = AnswerRequest {
            identifier: exercise.identifier,
            answer,
            skip: false,
        };
        client.wait(answer_delay(config))?;
        stats.answers_submitted += 1;
        // Exactly one call. In particular, never recover by resending after an
        // HTTP, parse, timeout, missing-feedback, or unexpected-acceptance error.
        let result = client.answer(lesson_id, &request)?;
        if is_flashcard {
            let feedback = result.feedback.as_ref().ok_or_else(|| {
                Error::parsing(
                    "Lesson API returned no answer feedback. Stopped without resubmitting.",
                )
            })?;
            if intentional_error && feedback.result > 0 {
                return Err(Error::parsing(
                    "The intentional wrong answer was unexpectedly accepted.",
                ));
            }
            if feedback.result <= 0 {
                stats.errors_made += 1;
            }
            if !intentional_error {
                if feedback.result <= 0 {
                    progress.candidate_index += 1;
                } else {
                    acknowledged.insert(fingerprint);
                    stats.words_learned += 1;
                }
            }
            client.progress(
                &prompt,
                feedback.result > 0,
                intentional_error,
                stats.words_learned,
            )?;
        } else {
            // New-word acknowledgements need no grading feedback. Any successful
            // response confirms the POST, so the same card must not be replayed.
            acknowledged.insert(fingerprint);
        }
        // Even a rejection can advance or finish the lesson. Never reconstruct
        // the next step from the previous prompt or from our answer's result.
        step = result.step;
    }
}

fn validate(props: LearningProps) -> Result<(u64, LearningStep)> {
    if props.reason.as_deref() == Some("limit-exceeded") {
        return Err(Error::limit(
            "Lingos reported that today's lesson limit has been reached.",
        ));
    }
    if props.status != "ok" || props.lesson_id == 0 {
        return Err(Error::parsing(
            "Could not read a valid lesson API state after navigation.",
        ));
    }
    let step = props
        .step
        .ok_or_else(|| Error::parsing("Lesson API returned no next step."))?;
    Ok((props.lesson_id, step))
}

fn validate_config(config: &Config) -> Result<()> {
    if config.lesson_prompt_safety_cap == 0 {
        return Err(Error::parsing("Lesson prompt safety cap must be positive."));
    }
    if config.errors_per100_words > 100 {
        return Err(Error::parsing(
            "Errors per 100 words must be between 0 and 100.",
        ));
    }
    if config.answer_delay_min_ms > config.answer_delay_max_ms {
        return Err(Error::parsing(
            "Minimum answer delay must not exceed the maximum.",
        ));
    }
    Ok(())
}

fn make_error(rate: u8, perfectionism: bool) -> bool {
    !perfectionism && rate > 0 && fastrand::u8(0..100) < rate
}

fn answer_delay(config: &Config) -> Duration {
    let min = config.answer_delay_min_ms;
    let max = config.answer_delay_max_ms;
    // The default fast path does not sample randomness or sleep. Keep the
    // client wait hook even for zero, so deadlines are still checked per POST.
    if min == max {
        return Duration::from_millis(min);
    }
    // Two bounded samples give a gentle, humanlike central tendency rather than
    // a fixed cadence. Overflow-safe even for the full u64 range.
    let first = fastrand::u64(min..=max);
    let second = fastrand::u64(min..=max);
    Duration::from_millis(first.min(second) + first.abs_diff(second) / 2)
}

fn wrong_answer(answer: &str, candidates: &[String]) -> String {
    let mut characters: Vec<char> = answer.chars().collect();
    if characters.is_empty() {
        return "wrong".into();
    }
    let index = fastrand::usize(..characters.len());
    let original = characters[index];
    loop {
        let replacement = fastrand::char('a'..='z');
        if !replacement.eq_ignore_ascii_case(&original) {
            characters[index] = replacement;
            break;
        }
    }
    let mut wrong: String = characters.into_iter().collect();
    // A one-letter mutation can itself be another stored correct answer.
    // Ensure the deliberate error never knowingly submits a valid candidate.
    while candidates
        .iter()
        .any(|candidate| normalized_key(candidate) == normalized_key(&wrong))
    {
        wrong.push('?');
    }
    wrong
}

#[cfg(test)]
#[path = "../tests/unit/lesson.rs"]
mod tests;
