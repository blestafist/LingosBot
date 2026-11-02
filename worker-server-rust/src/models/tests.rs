use super::*;
use serde_json::{Value, json};

#[test]
fn reads_camel_case_learning_payload_and_exercise_type() {
    let props: LearningProps = serde_json::from_value(json!({
        "status": "ok", "lessonId": 12,
        "step": {
            "state": "exercise", "continueUrl": "/learning/next",
            "exercise": {"type": "flashcard", "identifier": 3, "main": "dom"}
        }
    }))
    .unwrap();
    assert_eq!(props.lesson_id, 12);
    assert_eq!(props.reason, None);
    let step = props.step.unwrap();
    assert_eq!(step.continue_url.as_deref(), Some("/learning/next"));
    assert_eq!(step.exercise.unwrap().kind, "flashcard");
}

#[test]
fn compact_limit_payload_does_not_need_a_lesson() {
    for value in [
        json!({"reason": "limit-exceeded"}),
        json!({"status": "error", "reason": "limit-exceeded"}),
    ] {
        let props: LearningProps = serde_json::from_value(value).unwrap();
        assert_eq!(props.reason.as_deref(), Some("limit-exceeded"));
        assert_eq!(props.lesson_id, 0);
        assert!(props.step.is_none());
    }
}

#[test]
fn ordinary_payloads_require_a_lesson_identifier() {
    for value in [
        json!({}),
        json!({"status": "ok", "step": {"state": "finished"}}),
    ] {
        assert!(serde_json::from_value::<LearningProps>(value).is_err());
    }
}

#[test]
fn optional_fields_may_be_absent_or_null() {
    let props: LearningProps = serde_json::from_value(json!({"lessonId": 1})).unwrap();
    assert!(props.status.is_empty());
    assert!(props.step.is_none());
    let result: AnswerResult =
        serde_json::from_value(json!({"step": {"state": "finished"}})).unwrap();
    assert!(result.feedback.is_none());
    assert!(result.step.continue_url.is_none());
    assert!(result.step.exercise.is_none());
    let result: AnswerResult = serde_json::from_value(json!({
        "step": {"state": "finished", "exercise": null, "continueUrl": null},
        "feedback": null
    }))
    .unwrap();
    assert!(result.feedback.is_none());
}

#[test]
fn required_answer_step_and_feedback_result_do_not_default() {
    for value in [json!({}), json!({"step": null}), json!({"step": {}})] {
        assert!(serde_json::from_value::<AnswerResult>(value).is_err());
    }
    assert!(serde_json::from_value::<LearningFeedback>(json!({})).is_err());
    for value in [
        json!({"identifier": 1, "main": "dom"}),
        json!({"type": "flashcard", "main": "dom"}),
        json!({"type": "flashcard", "identifier": 1}),
        json!({"type": "flashcard", "identifier": -1, "main": "dom"}),
    ] {
        assert!(serde_json::from_value::<LearningExercise>(value).is_err());
    }
}

#[test]
fn request_serializes_the_exact_post_contract() {
    let request = AnswerRequest {
        identifier: 17,
        answer: "house".into(),
        skip: false,
    };
    assert_eq!(
        serde_json::to_value(request).unwrap(),
        json!({"identifier": 17, "answer": "house", "skip": false})
    );
    let exercise = LearningExercise {
        kind: "flashcard".into(),
        identifier: 17,
        main: "dom".into(),
    };
    let value = serde_json::to_value(exercise).unwrap();
    assert_eq!(value["type"], "flashcard");
    assert_eq!(value.get("kind"), None::<&Value>);
}
