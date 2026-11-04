use serde_json::json;
use worker_server_rust::challenge::{is_perfectionism, parse_challenges};

#[test]
fn any_strict_limit_keeps_perfectionism_protection() {
    for description in [
        "Lesson: max 1 error, max 2 errors",
        "Lesson: max 2 errors, max 1 error",
        "Lesson: bez błędów, max 2 errors",
        "Lesson: max 2 errors, bez błędów",
        "Lesson: max 1 error, bez błędów",
        "Lesson: max 2 errors, max zero errors",
    ] {
        assert!(
            is_perfectionism("Perfekcjonizm", description),
            "{description}"
        );
        let snapshot = parse_challenges(&json!({"challenges": [{
            "id": 1, "title": "Perfekcjonizm", "description": description,
            "prize": 5, "status": "in_progress"
        }]}))
        .unwrap();
        assert!(snapshot.active_perfectionism().is_some(), "{description}");
    }
}

#[test]
fn strict_limits_still_require_the_other_matching_conditions() {
    for (title, description) in [
        ("Trening", "Lesson: bez błędów, max 2 errors"),
        ("Perfekcjonizm", "bez błędów, max 2 errors"),
        ("Nie perfekcjonizm", "Lesson: bez błędów, max 2 errors"),
        ("Perfekcjonizm", "Lesson: max 2 errors, max 3 errors"),
    ] {
        assert!(
            !is_perfectionism(title, description),
            "{title}: {description}"
        );
    }
}

#[test]
fn ambiguous_snapshots_are_rejected() {
    for (id, status) in [(1, "available"), (2, "in_progress")] {
        assert!(parse_challenges(&json!({"challenges": [
            {"id": 1, "title": "Trening", "description": "", "prize": 5, "status": "in_progress"},
            {"id": id, "title": "Perfekcjonizm", "description": "", "prize": 5, "status": status}
        ]})).is_err());
    }
}
