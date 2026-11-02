use super::*;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};

fn assert_rejects<T: DeserializeOwned>(value: Value) {
    let text = value.to_string();
    assert!(
        serde_json::from_value::<T>(value).is_err(),
        "{} accepted {text} via from_value",
        std::any::type_name::<T>()
    );
    assert!(
        serde_json::from_str::<T>(&text).is_err(),
        "{} accepted {text} via from_str",
        std::any::type_name::<T>()
    );
}

fn assert_duplicate_fields_rejected<T: DeserializeOwned>(payloads: &[&str]) {
    for payload in payloads {
        let error = serde_json::from_str::<T>(payload).err().unwrap();
        assert!(error.to_string().contains("duplicate field"), "{error}");
    }
}

fn assert_requires_object<T: DeserializeOwned>(positional: Value) {
    for value in [
        positional,
        json!([]),
        json!(null),
        json!(true),
        json!(1),
        json!("exercise"),
    ] {
        assert_rejects::<T>(value);
    }
}

fn assert_object_roundtrip<T: DeserializeOwned + Serialize>(value: Value) {
    let model: T = serde_json::from_str(&value.to_string()).unwrap();
    assert_eq!(serde_json::to_value(model).unwrap(), value);
}

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
        json!({"reason": "limit-exceeded", "lessonId": null, "step": null}),
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
        json!({"lessonId": null}),
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
    let props: LearningProps =
        serde_json::from_value(json!({"lessonId": 1, "reason": null, "step": null})).unwrap();
    assert!(props.reason.is_none());
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
fn positional_arrays_are_not_model_objects() {
    assert_requires_object::<LearningProps>(json!(["ok", null, 1, {"state": "finished"}]));
    assert_rejects::<LearningProps>(json!(["error", "limit-exceeded", null, null]));
    assert_requires_object::<LearningStep>(json!(["exercise", null, {
        "type": "flashcard", "identifier": 1, "main": "dom"
    }]));
    assert_requires_object::<LearningExercise>(json!(["flashcard", 1, "dom"]));
    assert_requires_object::<AnswerRequest>(json!([1, "dom", false]));
    assert_requires_object::<AnswerResult>(json!([{"state": "finished"}, null]));
    assert_requires_object::<LearningFeedback>(json!([0]));
}

#[test]
fn direct_deserialize_calls_also_require_objects() {
    assert!(LearningProps::deserialize(json!(["ok", null, 1, null])).is_err());
    assert!(LearningStep::deserialize(json!(["finished", null, null])).is_err());
    assert!(LearningExercise::deserialize(json!(["flashcard", 1, "dom"])).is_err());
    assert!(AnswerRequest::deserialize(json!([1, "dom", false])).is_err());
    assert!(AnswerResult::deserialize(json!([{"state": "finished"}, null])).is_err());
    assert!(LearningFeedback::deserialize(json!([0])).is_err());
}

#[test]
fn nested_positional_arrays_are_not_model_objects() {
    for value in [
        json!({"lessonId": 1, "step": ["finished", null, null]}),
        json!({"lessonId": 1, "step": {
            "state": "exercise", "exercise": ["flashcard", 1, "dom"]
        }}),
        json!({"reason": "limit-exceeded", "step": ["finished", null, null]}),
    ] {
        assert_rejects::<LearningProps>(value);
    }
    for value in [
        json!({"step": ["finished", null, null]}),
        json!({"step": {
            "state": "exercise", "exercise": ["flashcard", 1, "dom"]
        }}),
        json!({"step": {"state": "finished"}, "feedback": [0]}),
        json!({"step": ["exercise", null, ["flashcard", 1, "dom"]], "feedback": [0]}),
    ] {
        assert_rejects::<AnswerResult>(value);
    }
}

#[test]
fn duplicate_fields_remain_errors_including_null_optional_fields() {
    assert_duplicate_fields_rejected::<LearningProps>(&[
        r#"{"lessonId":1,"status":"ok","status":"error"}"#,
        r#"{"lessonId":1,"reason":null,"reason":"limit-exceeded"}"#,
        r#"{"lessonId":1,"lessonId":2}"#,
        r#"{"lessonId":1,"step":null,"step":{"state":"finished"}}"#,
    ]);
    assert_duplicate_fields_rejected::<LearningStep>(&[
        r#"{"state":"exercise","state":"finished"}"#,
        r#"{"state":"finished","continueUrl":null,"continueUrl":"/next"}"#,
        r#"{"state":"finished","exercise":null,"exercise":null}"#,
    ]);
    assert_duplicate_fields_rejected::<LearningExercise>(&[
        r#"{"type":"flashcard","type":"other","identifier":1,"main":"dom"}"#,
        r#"{"type":"flashcard","identifier":1,"identifier":2,"main":"dom"}"#,
        r#"{"type":"flashcard","identifier":1,"main":"dom","main":"house"}"#,
    ]);
    assert_duplicate_fields_rejected::<AnswerRequest>(&[
        r#"{"identifier":1,"identifier":2,"answer":"dom","skip":false}"#,
        r#"{"identifier":1,"answer":"dom","answer":"house","skip":false}"#,
        r#"{"identifier":1,"answer":"dom","skip":false,"skip":true}"#,
    ]);
    assert_duplicate_fields_rejected::<AnswerResult>(&[
        r#"{"step":{"state":"exercise"},"step":{"state":"finished"}}"#,
        r#"{"step":{"state":"finished"},"feedback":null,"feedback":{"result":0}}"#,
        r#"{"step":{"state":"finished","state":"exercise"}}"#,
        r#"{"step":{"state":"finished"},"feedback":{"result":0,"result":1}}"#,
    ]);
    assert_duplicate_fields_rejected::<LearningFeedback>(&[r#"{"result":0,"result":1}"#]);
}

#[test]
fn model_object_serialization_contracts_are_unchanged() {
    assert_object_roundtrip::<LearningProps>(json!({
        "status": "ok", "reason": null, "lessonId": 12,
        "step": {
            "state": "exercise", "continueUrl": "/learning/next",
            "exercise": {"type": "flashcard", "identifier": 1, "main": "dom"}
        }
    }));
    assert_object_roundtrip::<LearningStep>(json!({
        "state": "finished", "continueUrl": null, "exercise": null
    }));
    assert_object_roundtrip::<LearningExercise>(json!({
        "type": "flashcard", "identifier": 1, "main": "dom"
    }));
    assert_object_roundtrip::<AnswerRequest>(json!({
        "identifier": 1, "answer": "house", "skip": false
    }));
    assert_object_roundtrip::<AnswerResult>(json!({
        "step": {"state": "finished", "continueUrl": null, "exercise": null},
        "feedback": {"result": 0}
    }));
    assert_object_roundtrip::<LearningFeedback>(json!({"result": 0}));
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
