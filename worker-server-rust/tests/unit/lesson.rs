use std::collections::VecDeque;

use super::*;
use crate::models::{LearningExercise, LearningFeedback};

#[derive(Default)]
struct MockClient {
    results: VecDeque<Result<AnswerResult>>,
    pages: VecDeque<Result<LearningProps>>,
    requests: Vec<(u64, AnswerRequest)>,
    navigations: Vec<String>,
    waits: Vec<Duration>,
    wait_error: Option<Error>,
    progress: Vec<(String, bool, bool, u64)>,
}

impl LessonClient for MockClient {
    fn navigate(&mut self, url: &str) -> Result<LearningProps> {
        self.navigations.push(url.into());
        self.pages.pop_front().expect("unexpected navigation")
    }

    fn answer(&mut self, lesson_id: u64, request: &AnswerRequest) -> Result<AnswerResult> {
        self.requests.push((lesson_id, request.clone()));
        self.results
            .pop_front()
            .expect("unexpected answer submission")
    }

    fn wait(&mut self, duration: Duration) -> Result<()> {
        self.waits.push(duration);
        match self.wait_error.take() {
            Some(error) => Err(error),
            None => Ok(()),
        }
    }

    fn progress(
        &mut self,
        prompt: &str,
        correct: bool,
        intentional: bool,
        words_learned: u64,
    ) -> Result<()> {
        self.progress
            .push((prompt.into(), correct, intentional, words_learned));
        Ok(())
    }
}

fn config() -> Config {
    Config {
        lesson_prompt_safety_cap: 30,
        errors_per100_words: 0,
        answer_delay_min_ms: 0,
        answer_delay_max_ms: 0,
        ..Config::default()
    }
}

fn vocabulary() -> Vocabulary {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("house", "dom").unwrap();
    vocabulary.insert("home", "dom").unwrap();
    vocabulary
}

fn exercise(id: u64, kind: &str, main: &str) -> LearningStep {
    LearningStep {
        state: "exercise".into(),
        continue_url: None,
        exercise: Some(LearningExercise {
            kind: kind.into(),
            identifier: id,
            main: main.into(),
        }),
    }
}

fn flashcard(id: u64) -> LearningStep {
    exercise(id, "flashcard", "dom")
}

fn state(name: &str) -> LearningStep {
    LearningStep {
        state: name.into(),
        continue_url: None,
        exercise: None,
    }
}

fn transition() -> LearningStep {
    LearningStep {
        state: "change_wordset".into(),
        continue_url: Some("/learning/next".into()),
        exercise: None,
    }
}

fn props(step: LearningStep, lesson_id: u64) -> LearningProps {
    LearningProps {
        status: "ok".into(),
        reason: None,
        lesson_id,
        step: Some(step),
    }
}

fn result(step: LearningStep, grade: Option<i32>) -> Result<AnswerResult> {
    Ok(AnswerResult {
        step,
        feedback: grade.map(|result| LearningFeedback { result }),
    })
}

fn limit_props() -> LearningProps {
    LearningProps {
        status: "error".into(),
        reason: Some("limit-exceeded".into()),
        lesson_id: 0,
        step: None,
    }
}

fn run(client: &mut MockClient, config: &Config, step: LearningStep) -> Result<LessonStats> {
    run_lesson(client, config, &vocabulary(), false, props(step, 12))
}

fn submitted(client: &MockClient) -> Vec<&str> {
    client
        .requests
        .iter()
        .map(|(_, request)| request.answer.as_str())
        .collect()
}

#[test]
fn progress_reports_graded_flashcards_and_cumulative_learned_words() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(flashcard(1), Some(-1)),
        result(exercise(2, "new_teacher", "new"), Some(2)),
        result(exercise(3, "new_premium", "new"), Some(0)),
        result(state("finished"), None),
    ]);

    let stats = run(
        &mut client,
        &settings,
        exercise(1, "flashcard", " \tdom\u{a0}"),
    )
    .unwrap();
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 1,
            answers_submitted: 5,
            errors_made: 2,
        }
    );
    assert_eq!(
        client.progress,
        [
            ("dom".into(), false, true, 0),
            ("dom".into(), false, false, 0),
            ("dom".into(), true, false, 1),
        ]
    );
}

#[test]
fn progress_and_error_counts_continue_across_wordset_transitions() {
    let mut client = MockClient::default();
    client.results.extend([
        result(transition(), Some(1)),
        result(exercise(2, "flashcard", "house"), Some(0)),
        result(state("finished"), Some(2)),
    ]);
    client.pages.push_back(Ok(props(flashcard(1), 99)));

    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 3,
            errors_made: 1,
        }
    );
    assert_eq!(
        client.progress,
        [
            ("dom".into(), true, false, 1),
            ("dom".into(), false, false, 1),
            ("house".into(), true, false, 2),
        ]
    );
}

#[test]
fn progress_is_not_reported_for_unconfirmed_or_unexpected_answers() {
    let mut failed_post = MockClient::default();
    failed_post
        .results
        .push_back(Err(Error::parsing("timeout")));
    assert!(run(&mut failed_post, &config(), flashcard(1)).is_err());
    assert!(failed_post.progress.is_empty());

    let mut missing_feedback = MockClient::default();
    missing_feedback
        .results
        .push_back(result(state("finished"), None));
    assert!(run(&mut missing_feedback, &config(), flashcard(1)).is_err());
    assert!(missing_feedback.progress.is_empty());

    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut unexpected_acceptance = MockClient::default();
    unexpected_acceptance
        .results
        .push_back(result(state("finished"), Some(1)));
    assert!(run(&mut unexpected_acceptance, &settings, flashcard(1)).is_err());
    assert!(unexpected_acceptance.progress.is_empty());
}

#[test]
fn finished_initial_state_needs_no_vocabulary_or_requests() {
    let mut client = MockClient::default();
    let stats = run_lesson(
        &mut client,
        &config(),
        &Vocabulary::new(),
        false,
        props(state("finished"), 12),
    )
    .unwrap();
    assert_eq!(stats, LessonStats::default());
    assert!(client.requests.is_empty());
    assert!(client.navigations.is_empty());
    assert!(client.waits.is_empty());
    assert!(client.progress.is_empty());
}

#[test]
fn identical_prompts_with_new_identifiers_are_distinct_words() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(2), Some(1)),
        result(state("finished"), Some(2)),
    ]);
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 2,
            errors_made: 0,
        }
    );
    assert_eq!(submitted(&client), ["house", "house"]);
    assert_eq!(
        client
            .requests
            .iter()
            .map(|(_, r)| r.identifier)
            .collect::<Vec<_>>(),
        [1, 2]
    );
    assert!(client.requests.iter().all(|(_, r)| !r.skip));
    assert!(client.navigations.is_empty());
}

#[test]
fn confirmed_rejection_allows_an_alternative_only_for_the_same_exercise() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(flashcard(2), Some(-1)),
        result(state("finished"), Some(1)),
    ]);
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(submitted(&client), ["house", "home", "house"]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 1,
            answers_submitted: 3,
            errors_made: 2,
        }
    );
}

#[test]
fn rejected_flashcard_resumes_candidates_after_another_exercise() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(2), Some(0)),
        result(flashcard(1), Some(1)),
        result(state("finished"), Some(1)),
    ]);
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(submitted(&client), ["house", "house", "home"]);
    assert_eq!(
        client
            .requests
            .iter()
            .map(|(_, request)| request.identifier)
            .collect::<Vec<_>>(),
        [1, 2, 1]
    );
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 3,
            errors_made: 1,
        }
    );
    assert_eq!(client.waits.len(), 3);
    assert!(client.navigations.is_empty());
}

#[test]
fn accepted_flashcard_is_not_submitted_again() {
    let mut client = MockClient::default();
    client.results.push_back(result(flashcard(1), Some(1)));
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("already acknowledged"));
    assert_eq!(submitted(&client), ["house"]);
    assert_eq!(client.waits.len(), 1);
    assert!(client.navigations.is_empty());
}

#[test]
fn accepted_flashcard_is_not_replayed_after_another_exercise() {
    let mut client = MockClient::default();
    client
        .results
        .extend([result(flashcard(2), Some(1)), result(flashcard(1), Some(1))]);
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("already acknowledged"));
    assert_eq!(submitted(&client), ["house", "house"]);
    assert_eq!(client.waits.len(), 2);
}

#[test]
fn repeated_main_with_normalized_whitespace_is_still_acknowledged() {
    let mut client = MockClient::default();
    client
        .results
        .push_back(result(exercise(1, "flashcard", " \tdom\u{a0}"), Some(1)));
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("already acknowledged"));
    assert_eq!(client.requests.len(), 1);
    assert_eq!(client.waits.len(), 1);
}

#[test]
fn changed_main_with_the_same_identifier_resets_answer_candidates() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(exercise(1, "flashcard", "house"), Some(0)),
        result(state("finished"), Some(1)),
    ]);
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(submitted(&client), ["house", "home", "dom"]);
    assert_eq!(stats.words_learned, 1);
}

#[test]
fn changed_main_with_the_same_identifier_resets_error_consideration() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.extend([
        result(exercise(1, "flashcard", "house"), Some(0)),
        result(state("finished"), Some(0)),
    ]);
    let stats = run(&mut client, &settings, flashcard(1)).unwrap();
    let answers = submitted(&client);
    assert!(!["house", "home"].contains(&answers[0]));
    assert_ne!(answers[1], "dom");
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 0,
            answers_submitted: 2,
            errors_made: 2,
        }
    );
}

#[test]
fn changed_main_with_the_same_identifier_is_a_distinct_learned_exercise() {
    let mut client = MockClient::default();
    client.results.extend([
        result(exercise(1, "flashcard", "house"), Some(1)),
        result(state("finished"), Some(1)),
    ]);
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(submitted(&client), ["house", "dom"]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 2,
            errors_made: 0,
        }
    );
}

#[test]
fn changed_type_with_the_same_identifier_preserves_prior_error_consideration() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.extend([
        result(exercise(1, "new_teacher", "dom"), Some(0)),
        result(flashcard(1), None),
        result(state("finished"), Some(1)),
    ]);
    let stats = run(&mut client, &settings, flashcard(1)).unwrap();
    let answers = submitted(&client);
    assert!(!["house", "home"].contains(&answers[0]));
    assert_eq!(answers[1], "");
    assert_eq!(answers[2], "house");
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 1,
            answers_submitted: 3,
            errors_made: 1,
        }
    );
}

#[test]
fn exhausted_candidates_stop_before_another_post_or_delay() {
    let mut client = MockClient::default();
    client
        .results
        .extend([result(flashcard(1), Some(0)), result(flashcard(1), Some(0))]);
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("rejected"));
    assert_eq!(submitted(&client), ["house", "home"]);
    assert_eq!(client.waits.len(), 2);
}

#[test]
fn exhausted_candidates_stay_exhausted_after_another_exercise() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(flashcard(2), Some(0)),
        result(flashcard(1), Some(1)),
    ]);
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(
        error
            .to_string()
            .contains("All 2 stored answers were rejected")
    );
    assert_eq!(submitted(&client), ["house", "home", "house"]);
    assert_eq!(client.waits.len(), 3);
    assert!(client.navigations.is_empty());
}

#[test]
fn intentional_error_is_injected_once_and_does_not_consume_a_candidate() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(flashcard(1), Some(0)),
        result(state("finished"), Some(1)),
    ]);
    let stats = run(&mut client, &settings, flashcard(1)).unwrap();
    let answers = submitted(&client);
    assert!(!["house", "home"].contains(&answers[0]));
    assert_eq!(&answers[1..], ["house", "home"]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 1,
            answers_submitted: 3,
            errors_made: 2,
        }
    );
}

#[test]
fn intentional_error_is_not_repeated_after_another_exercise() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(2), Some(0)),
        result(flashcard(2), Some(0)),
        result(flashcard(1), Some(1)),
        result(state("finished"), Some(1)),
    ]);
    let stats = run(&mut client, &settings, flashcard(1)).unwrap();
    let answers = submitted(&client);
    assert!(!["house", "home"].contains(&answers[0]));
    assert!(!["house", "home"].contains(&answers[1]));
    assert_eq!(&answers[2..], ["house", "house"]);
    assert_eq!(
        client
            .requests
            .iter()
            .map(|(_, request)| request.identifier)
            .collect::<Vec<_>>(),
        [1, 2, 2, 1]
    );
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 4,
            errors_made: 2,
        }
    );
    assert_eq!(client.waits.len(), 4);
    assert!(client.navigations.is_empty());
}

#[test]
fn intentional_rejections_follow_the_server_without_replaying_old_prompts() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(2), Some(0)),
        result(state("finished"), Some(0)),
    ]);
    let stats = run(&mut client, &settings, flashcard(1)).unwrap();
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 0,
            answers_submitted: 2,
            errors_made: 2,
        }
    );
    assert_eq!(
        client
            .requests
            .iter()
            .map(|(_, r)| r.identifier)
            .collect::<Vec<_>>(),
        [1, 2]
    );
    assert!(
        submitted(&client)
            .iter()
            .all(|answer| !["house", "home"].contains(answer))
    );
}

#[test]
fn perfectionism_disables_even_a_hundred_percent_error_rate() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.push_back(result(state("finished"), Some(1)));
    let stats = run_lesson(
        &mut client,
        &settings,
        &vocabulary(),
        true,
        props(flashcard(1), 12),
    )
    .unwrap();
    assert_eq!(submitted(&client), ["house"]);
    assert_eq!(stats.words_learned, 1);
}

#[test]
fn unexpectedly_accepted_intentional_error_stops_without_following_the_next_step() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.push_back(result(transition(), Some(1)));
    let error = run(&mut client, &settings, flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("unexpectedly accepted"));
    assert_eq!(client.requests.len(), 1);
    assert!(client.navigations.is_empty());
}

#[test]
fn new_word_cards_are_acknowledged_without_injecting_errors_or_learning_stats() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    for kind in ["new_teacher", "new_premium"] {
        for feedback in [None, Some(0), Some(1)] {
            let mut client = MockClient::default();
            client
                .results
                .push_back(result(state("finished"), feedback));
            let stats = run_lesson(
                &mut client,
                &settings,
                &Vocabulary::new(),
                false,
                props(exercise(1, kind, ""), 12),
            )
            .unwrap();
            assert_eq!(submitted(&client), [""]);
            assert_eq!(
                stats,
                LessonStats {
                    words_learned: 0,
                    answers_submitted: 1,
                    errors_made: 0,
                }
            );
        }
    }
}

#[test]
fn new_word_payloads_are_acknowledged_on_entry_and_in_answer_responses() {
    let initial = crate::workflow::parse_learning_page(serde_json::json!({
        "status": "ok", "lessonId": 17581846,
        "leaveUrl": "/learning/leave/dashboard",
        "step": {
            "state": "exercise", "progress": {"current": 10, "total": 20},
            "exercise": {
                "type": "new_teacher", "identifier": 335171564,
                "word": "probiere eine Massage aus",
                "translation": "wypróbuj masaż", "context": ""
            }
        }
    }))
    .unwrap();
    let next: AnswerResult = serde_json::from_value(serde_json::json!({
        "step": {"state": "exercise", "exercise": {
            "type": "new_premium", "identifier": 335171565,
            "word": "nächstes Wort", "translation": "następne słowo", "context": ""
        }}
    }))
    .unwrap();
    let mut client = MockClient::default();
    client
        .results
        .extend([Ok(next), result(state("finished"), None)]);
    let mut settings = config();
    settings.errors_per100_words = 100;

    let stats = run_lesson(&mut client, &settings, &Vocabulary::new(), false, initial).unwrap();

    assert_eq!(submitted(&client), ["", ""]);
    assert!(
        client
            .requests
            .iter()
            .all(|(id, request)| *id == 17581846 && !request.skip)
    );
    assert_eq!(client.requests[0].1.identifier, 335171564);
    assert_eq!(client.requests[1].1.identifier, 335171565);
    assert_eq!(
        stats,
        LessonStats {
            answers_submitted: 2,
            ..LessonStats::default()
        }
    );
    assert!(client.progress.is_empty());
}

#[test]
fn new_word_acknowledgements_need_no_text_fields() {
    for kind in ["new_teacher", "new_premium"] {
        let step: LearningStep = serde_json::from_value(serde_json::json!({
            "state": "exercise", "exercise": {"type": kind, "identifier": 1}
        }))
        .unwrap();
        let mut client = MockClient::default();
        client.results.push_back(result(state("finished"), None));

        let stats = run(&mut client, &config(), step).unwrap();

        assert_eq!(submitted(&client), [""]);
        assert_eq!(stats.words_learned, 0);
    }
}

#[test]
fn flashcards_without_main_and_unknown_types_stop_before_posting() {
    for kind in ["flashcard", "unknown"] {
        let step: LearningStep = serde_json::from_value(serde_json::json!({
            "state": "exercise", "exercise": {
                "type": kind, "identifier": 1, "word": "house", "translation": "dom"
            }
        }))
        .unwrap();
        let mut client = MockClient::default();

        assert!(run(&mut client, &config(), step).is_err());
        assert!(client.requests.is_empty());
    }
}

#[test]
fn acknowledged_new_word_cards_are_not_submitted_again() {
    for kind in ["new_teacher", "new_premium"] {
        for feedback in [None, Some(0), Some(1)] {
            let step = exercise(1, kind, "dom");
            let mut client = MockClient::default();
            client.results.push_back(result(step.clone(), feedback));
            let error = run(&mut client, &config(), step).unwrap_err();
            assert!(error.to_string().contains("already acknowledged"));
            assert_eq!(submitted(&client), [""]);
            assert_eq!(client.waits.len(), 1);
            assert!(client.navigations.is_empty());
        }
    }
}

#[test]
fn different_new_word_types_with_the_same_identifier_are_distinct_acknowledgements() {
    let mut client = MockClient::default();
    client.results.extend([
        result(exercise(1, "new_premium", "dom"), None),
        result(state("finished"), None),
    ]);
    let stats = run(&mut client, &config(), exercise(1, "new_teacher", "dom")).unwrap();
    assert_eq!(submitted(&client), ["", ""]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 0,
            answers_submitted: 2,
            errors_made: 0,
        }
    );
}

#[test]
fn a_mixed_lesson_counts_correct_flashcards_not_rejections_or_new_cards() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(exercise(2, "new_teacher", "new"), Some(1)),
        result(exercise(3, "new_premium", "new"), None),
        result(flashcard(4), Some(1)),
        result(state("finished"), Some(0)),
    ]);
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 1,
            answers_submitted: 5,
            errors_made: 2,
        }
    );
}

#[test]
fn wordset_transition_reads_a_fresh_lesson_id_and_resets_candidates() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(transition(), Some(1)),
        result(state("finished"), Some(1)),
    ]);
    client.pages.push_back(Ok(props(flashcard(1), 99)));
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(client.navigations, ["/learning/next"]);
    assert_eq!(
        client
            .requests
            .iter()
            .map(|(id, _)| *id)
            .collect::<Vec<_>>(),
        [12, 12, 99]
    );
    assert_eq!(submitted(&client), ["house", "home", "house"]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 3,
            errors_made: 1,
        }
    );
}

#[test]
fn wordset_transition_clears_exhausted_candidates_for_the_same_fingerprint() {
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(flashcard(2), Some(0)),
        result(transition(), Some(1)),
        result(state("finished"), Some(1)),
    ]);
    client.pages.push_back(Ok(props(flashcard(1), 12)));
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(submitted(&client), ["house", "home", "house", "house"]);
    assert!(
        client
            .requests
            .iter()
            .all(|(lesson_id, _)| *lesson_id == 12)
    );
    assert_eq!(client.navigations, ["/learning/next"]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 4,
            errors_made: 2,
        }
    );
    assert_eq!(client.waits.len(), 4);
}

#[test]
fn wordset_transition_resets_intentional_error_for_the_same_fingerprint() {
    let mut settings = config();
    settings.errors_per100_words = 100;
    let mut client = MockClient::default();
    client.results.extend([
        result(transition(), Some(0)),
        result(flashcard(1), Some(0)),
        result(state("finished"), Some(1)),
    ]);
    client.pages.push_back(Ok(props(flashcard(1), 12)));
    let stats = run(&mut client, &settings, flashcard(1)).unwrap();
    let answers = submitted(&client);
    assert!(!["house", "home"].contains(&answers[0]));
    assert!(!["house", "home"].contains(&answers[1]));
    assert_eq!(answers[2], "house");
    assert!(
        client
            .requests
            .iter()
            .all(|(lesson_id, _)| *lesson_id == 12)
    );
    assert_eq!(client.navigations, ["/learning/next"]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 1,
            answers_submitted: 3,
            errors_made: 2,
        }
    );
    assert_eq!(client.waits.len(), 3);
}

#[test]
fn identical_exercise_reused_after_a_wordset_transition_is_counted_again() {
    let mut client = MockClient::default();
    client.results.extend([
        result(transition(), Some(1)),
        result(state("finished"), Some(1)),
    ]);
    client.pages.push_back(Ok(props(flashcard(1), 12)));
    let stats = run(&mut client, &config(), flashcard(1)).unwrap();
    assert_eq!(submitted(&client), ["house", "house"]);
    assert_eq!(client.navigations, ["/learning/next"]);
    assert_eq!(
        stats,
        LessonStats {
            words_learned: 2,
            answers_submitted: 2,
            errors_made: 0,
        }
    );
}

#[test]
fn initial_and_transition_limits_are_distinct_limit_errors() {
    let mut client = MockClient::default();
    let error =
        run_lesson(&mut client, &config(), &vocabulary(), false, limit_props()).unwrap_err();
    assert!(error.is_limit());
    assert!(client.requests.is_empty());
    assert!(client.navigations.is_empty());

    client.pages.push_back(Ok(limit_props()));
    let error = run(&mut client, &config(), transition()).unwrap_err();
    assert!(error.is_limit());
    assert!(client.requests.is_empty());
    assert_eq!(client.navigations, ["/learning/next"]);
}

#[test]
fn api_limit_errors_are_propagated_without_retry() {
    let mut client = MockClient::default();
    client.results.push_back(Err(Error::limit("daily limit")));
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(error.is_limit());
    assert_eq!(client.requests.len(), 1);
    assert!(client.navigations.is_empty());
}

#[test]
fn ambiguous_post_failure_is_not_retried_or_followed_by_navigation() {
    let mut client = MockClient::default();
    client
        .results
        .push_back(Err(Error::parsing("ambiguous submission timeout")));
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("ambiguous submission timeout"));
    assert_eq!(client.requests.len(), 1);
    assert_eq!(client.waits.len(), 1);
    assert!(client.navigations.is_empty());
}

#[test]
fn missing_flashcard_feedback_stops_even_when_the_server_moves_or_finishes() {
    for next in [flashcard(1), flashcard(2), transition(), state("finished")] {
        let mut client = MockClient::default();
        client.results.push_back(result(next, None));
        let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
        assert!(error.to_string().contains("feedback"));
        assert_eq!(client.requests.len(), 1);
        assert!(client.navigations.is_empty());
    }
}

#[test]
fn unknown_states_select_level_and_unknown_exercises_stop_without_submissions() {
    for step in [
        state("unknown"),
        state("select_level"),
        state(""),
        exercise(1, "unknown", "dom"),
        exercise(0, "flashcard", "dom"),
        state("exercise"),
        exercise(1, "flashcard", " \t\u{a0}"),
        exercise(1, "flashcard", "unknown"),
    ] {
        let mut client = MockClient::default();
        assert!(run(&mut client, &config(), step).is_err());
        assert!(client.requests.is_empty());
        assert!(client.navigations.is_empty());
        assert!(client.waits.is_empty());
    }
}

#[test]
fn an_unknown_returned_step_is_not_replaced_with_the_previous_prompt() {
    let mut client = MockClient::default();
    client.results.push_back(result(state("unknown"), Some(0)));
    assert!(run(&mut client, &config(), flashcard(1)).is_err());
    assert_eq!(client.requests.len(), 1);
}

#[test]
fn select_level_error_explains_the_required_user_action() {
    let error = run(&mut MockClient::default(), &config(), state("select_level")).unwrap_err();
    assert!(error.to_string().contains("Choose your language level"));
}

#[test]
fn invalid_initial_and_transition_props_are_not_used() {
    let valid = props(flashcard(1), 12);
    let mut invalid = vec![
        LearningProps {
            status: "error".into(),
            ..valid.clone()
        },
        LearningProps {
            lesson_id: 0,
            ..valid.clone()
        },
        LearningProps {
            step: None,
            ..valid
        },
    ];
    for props in invalid.drain(..) {
        let mut client = MockClient::default();
        assert!(run_lesson(&mut client, &config(), &vocabulary(), false, props.clone()).is_err());
        assert!(client.requests.is_empty());
        client.pages.push_back(Ok(props));
        assert!(run(&mut client, &config(), transition()).is_err());
        assert!(client.requests.is_empty());
        assert_eq!(client.navigations.len(), 1);
    }
}

#[test]
fn navigation_failure_is_not_retried() {
    let mut client = MockClient::default();
    client
        .pages
        .push_back(Err(Error::parsing("navigation failed")));
    assert!(run(&mut client, &config(), transition()).is_err());
    assert_eq!(client.navigations.len(), 1);
    assert!(client.requests.is_empty());
}

#[test]
fn missing_or_blank_transition_url_is_not_navigated() {
    for continue_url in [None, Some(" \t\u{a0}".into())] {
        let mut client = MockClient::default();
        let mut step = transition();
        step.continue_url = continue_url;
        assert!(run(&mut client, &config(), step).is_err());
        assert!(client.navigations.is_empty());
    }
}

#[test]
fn prompt_cap_allows_final_completion_but_not_another_flashcard() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 1;
    let mut complete = MockClient::default();
    complete
        .results
        .push_back(result(state("finished"), Some(1)));
    assert_eq!(
        run(&mut complete, &settings, flashcard(1))
            .unwrap()
            .words_learned,
        1
    );

    let mut looping = MockClient::default();
    looping.results.push_back(result(flashcard(2), Some(1)));
    let error = run(&mut looping, &settings, flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("prompt safety cap"));
    assert_eq!(looping.requests.len(), 1);
}

#[test]
fn submission_cap_stops_new_word_loops_with_distinct_identifiers() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 1;
    for kind in ["new_teacher", "new_premium"] {
        let mut client = MockClient::default();
        for identifier in 2..=5 {
            client
                .results
                .push_back(result(exercise(identifier, kind, ""), None));
        }
        let error = run(&mut client, &settings, exercise(1, kind, "")).unwrap_err();
        assert!(error.to_string().contains("submission safety cap"));
        assert_eq!(client.requests.len(), 4);
        assert_eq!(client.waits.len(), 4);
    }
}

#[test]
fn completion_at_exact_submission_cap_is_allowed() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 1;
    let mut client = MockClient::default();
    for identifier in 2..=4 {
        client
            .results
            .push_back(result(exercise(identifier, "new_premium", ""), None));
    }
    client.results.push_back(result(state("finished"), None));
    let stats = run(&mut client, &settings, exercise(1, "new_premium", "")).unwrap();
    assert_eq!(stats.answers_submitted, 4);
}

#[test]
fn prompt_cap_counts_changed_main_with_the_same_identifier() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 1;
    let mut client = MockClient::default();
    client
        .results
        .push_back(result(exercise(1, "flashcard", "house"), Some(1)));
    let error = run(&mut client, &settings, flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("prompt safety cap"));
    assert_eq!(client.requests.len(), 1);
}

#[test]
fn prompt_cap_still_counts_a_returning_unacknowledged_flashcard() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 2;
    let mut client = MockClient::default();
    client
        .results
        .extend([result(flashcard(2), Some(0)), result(flashcard(1), Some(1))]);
    let error = run(&mut client, &settings, flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("prompt safety cap"));
    assert_eq!(submitted(&client), ["house", "house"]);
    assert_eq!(client.waits.len(), 2);
}

#[test]
fn prompt_cap_counts_flashcards_after_a_type_change_with_the_same_identifier() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 1;
    let mut client = MockClient::default();
    client
        .results
        .extend([result(flashcard(1), None), result(flashcard(2), Some(1))]);
    let error = run(&mut client, &settings, exercise(1, "new_teacher", "dom")).unwrap_err();
    assert!(error.to_string().contains("prompt safety cap"));
    assert_eq!(submitted(&client), ["", "house"]);
    assert_eq!(client.waits.len(), 2);
}

#[test]
fn transition_safety_cap_stops_a_navigation_loop() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 1;
    let mut client = MockClient::default();
    client.pages.push_back(Ok(props(transition(), 12)));
    let error = run(&mut client, &settings, transition()).unwrap_err();
    assert!(error.to_string().contains("transition safety cap"));
    assert_eq!(client.navigations.len(), 1);
    assert!(client.requests.is_empty());
}

#[test]
fn completion_at_exact_transition_cap_is_allowed() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = 1;
    let mut client = MockClient::default();
    client.pages.push_back(Ok(props(state("finished"), 99)));
    assert_eq!(
        run(&mut client, &settings, transition()).unwrap(),
        LessonStats::default()
    );
}

#[test]
fn large_caps_do_not_overflow() {
    let mut settings = config();
    settings.lesson_prompt_safety_cap = usize::MAX;
    let mut client = MockClient::default();
    client.results.push_back(result(state("finished"), Some(1)));
    assert_eq!(
        run(&mut client, &settings, flashcard(1))
            .unwrap()
            .answers_submitted,
        1
    );
}

#[test]
fn reverse_unicode_prompt_is_resolved_in_the_lesson() {
    let mut words = Vocabulary::new();
    words.insert("Łódź", "船").unwrap();
    let mut client = MockClient::default();
    client.results.push_back(result(state("finished"), Some(1)));
    let stats = run_lesson(
        &mut client,
        &config(),
        &words,
        false,
        props(exercise(1, "flashcard", "\u{a0}ŁÓDŹ\n"), 12),
    )
    .unwrap();
    assert_eq!(submitted(&client), ["船"]);
    assert_eq!(stats.words_learned, 1);
}

#[test]
fn wrong_answers_are_unicode_safe_and_not_stored_candidates() {
    for answer in ["猫", "Łódź", "🦀", "É", "house"] {
        let candidates = vec![answer.to_owned()];
        for _ in 0..30 {
            let wrong = wrong_answer(answer, &candidates);
            assert_ne!(wrong.to_uppercase(), answer.to_uppercase());
            assert!(!wrong.is_empty());
        }
    }
    let candidates: Vec<String> = ('a'..='z').map(|letter| letter.to_string()).collect();
    assert!(!candidates.contains(&wrong_answer("a", &candidates)));
    assert_eq!(wrong_answer("", &[]), "wrong");
}

#[test]
fn configured_delays_are_sent_through_the_client_hook_before_every_post() {
    let mut settings = config();
    settings.answer_delay_min_ms = 12;
    settings.answer_delay_max_ms = 41;
    let mut client = MockClient::default();
    client.results.extend([
        result(flashcard(1), Some(0)),
        result(exercise(2, "new_teacher", ""), Some(1)),
        result(state("finished"), None),
    ]);
    run(&mut client, &settings, flashcard(1)).unwrap();
    assert_eq!(client.waits.len(), 3);
    assert!(
        client
            .waits
            .iter()
            .all(|delay| (12..=41).contains(&(delay.as_millis() as u64)))
    );
}

#[test]
fn deadline_error_in_wait_prevents_the_post() {
    let mut client = MockClient {
        wait_error: Some(Error::parsing("global deadline reached")),
        ..MockClient::default()
    };
    let error = run(&mut client, &config(), flashcard(1)).unwrap_err();
    assert!(error.to_string().contains("global deadline reached"));
    assert_eq!(client.waits.len(), 1);
    assert!(client.requests.is_empty());
    assert!(client.navigations.is_empty());
}

#[test]
fn delay_sampling_respects_fixed_zero_and_extreme_bounds() {
    let mut settings = config();
    assert_eq!(answer_delay(&settings), Duration::ZERO);
    for bound in [1, 777, u64::MAX] {
        settings.answer_delay_min_ms = bound;
        settings.answer_delay_max_ms = bound;
        assert_eq!(answer_delay(&settings), Duration::from_millis(bound));
    }
    settings.answer_delay_min_ms = 0;
    for _ in 0..100 {
        assert!(answer_delay(&settings) <= Duration::from_millis(u64::MAX));
    }
}

#[test]
fn error_rate_extremes_and_perfectionism_are_deterministic() {
    for _ in 0..100 {
        assert!(!make_error(0, false));
        assert!(make_error(100, false));
        assert!(!make_error(100, true));
    }
}

#[test]
fn invalid_engine_config_stops_before_waiting_or_submitting() {
    let mut zero_cap = config();
    zero_cap.lesson_prompt_safety_cap = 0;
    let mut too_many_errors = config();
    too_many_errors.errors_per100_words = 101;
    let mut reversed_delay = config();
    reversed_delay.answer_delay_min_ms = 2;
    reversed_delay.answer_delay_max_ms = 1;
    for settings in [zero_cap, too_many_errors, reversed_delay] {
        let mut client = MockClient::default();
        assert!(run(&mut client, &settings, flashcard(1)).is_err());
        assert!(client.requests.is_empty());
        assert!(client.waits.is_empty());
    }
}
