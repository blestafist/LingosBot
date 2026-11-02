use serde_json::json;

use super::*;

fn challenge(id: i64, status: &str, prize: i32) -> Value {
    json!({
        "id": id, "title": "Perfekcjonizm", "description": "",
        "prize": prize, "status": status
    })
}

#[test]
fn desktop_semantic_matching_cases() {
    let cases = [
        (
            "Perfekcjonizm",
            "Wykonaj 1 lekcję z maksymalnie 1 błędem",
            true,
        ),
        (
            "  PERFEKCJONIZM  ",
            "Wykonaj   1 lekcje z MAKSYMALNIE 1 BŁĘDEM.",
            true,
        ),
        (
            "Perfekcjonizm II",
            "Wykonaj 3 lekcje z maksymalnie jednym błędem",
            true,
        ),
        (
            "Wyzwanie specjalne",
            "Perfekcjonizm: wykonaj 2 lekcje z maksymalnie 1 bledem",
            true,
        ),
        ("Perfekcjonizm", "Wykonaj 1 lekcję — do zera błędów", true),
        (
            "Perfekcjonizm",
            "Wykonaj 1 lekcję z maksymalnie dwóch błędów",
            false,
        ),
        ("Perfekcjonizm", "Wykonaj 1 lekcję bez błędów", true),
        (
            "Wyzwanie specjalne",
            "Perfekcjonizmu: wykonaj 1 lekcję bez błędów",
            true,
        ),
        (
            "Perfekcyjna lekcja",
            "Wykonaj 1 lekcję z maksymalnie 1 błędem",
            false,
        ),
        (
            "Wytrwałość",
            "To nie jest perfekcjonizm ani wyzwanie z błędami",
            false,
        ),
        (
            "Wytrwałość",
            "Nie perfekcjonizm: wykonaj 1 lekcję z maksymalnie 1 błędem",
            false,
        ),
        (
            "Perfekcjonizm",
            "Wykonaj 1 lekcję z maksymalnie 2 błędami",
            false,
        ),
    ];
    for (title, description, expected) in cases {
        assert_eq!(
            is_perfectionism(title, description),
            expected,
            "{title}: {description}"
        );
    }
}

#[test]
fn title_is_sufficient_but_description_only_requires_the_condition() {
    assert!(is_perfectionism("Perfekcjonizm", ""));
    assert!(!is_perfectionism("Trening", "Perfekcjonizm"));
    assert!(!is_perfectionism("Trening", "Perfekcjonizm: maks 1 błąd"));
    assert!(!is_perfectionism("Perfekcjonizm", "Maks 1 błąd"));
    assert!(is_perfectionism(
        "Perfekcjonizm",
        "Wykonaj\u{a0}1 lekcje z maksymalnie 1 bledem"
    ));
}

#[test]
fn normalized_regex_variants_and_word_boundaries() {
    for condition in [
        "maks 1 błąd",
        "max 0 errors",
        "maximum one error",
        "najwyżej jeden błąd",
        "nie więcej niż zero błędów",
        "do zera błędów",
        "at most one error",
        "no more than 1 error",
        "bez błędów",
    ] {
        assert!(is_perfectionism(
            "Perfekcjonizm",
            &format!("Lesson: {condition}")
        ));
    }
    for condition in ["max 2 errors", "max two errors", "max 2147483648 errors"] {
        assert!(!is_perfectionism(
            "Perfekcjonizm",
            &format!("Lesson: {condition}")
        ));
    }
    for negation in ["nie", "bez", "brak"] {
        assert!(!is_perfectionism(&format!("{negation} perfekcjonizmu"), ""));
    }
    assert!(!is_perfectionism(
        "Antyperfekcjonizm",
        "Lesson: max 1 error"
    ));
    assert!(!is_perfectionism(
        "Trening",
        "Perfekcjonizm: mylesson max 1 error"
    ));
    // The desktop regex intentionally uses the first limit, not the last.
    assert!(!is_perfectionism(
        "Perfekcjonizm",
        "Lesson max 2 errors, max 1 error"
    ));
}

#[test]
fn normalization_handles_polish_decomposed_accents_and_punctuation() {
    assert_eq!(normalize_text(" \tZażółć\u{a0}\n gęślą  "), "Zażółć gęślą");
    assert_eq!(
        normalize_for_matching(" ZAŻÓŁĆ—GE\u{328}ŚLĄ! "),
        "zazolc gesla"
    );
    assert_eq!(normalize_for_matching("\u{301} ... \t"), "");
}

#[test]
fn only_active_challenges_disable_intentional_errors() {
    for (status, expected) in [
        ("available", false),
        ("in_progress", true),
        ("completed", false),
    ] {
        let snapshot = parse_challenges(&json!({"challenges": [challenge(1, status, 5)]})).unwrap();
        assert_eq!(snapshot.challenges[0].is_perfectionism(), expected);
        assert_eq!(snapshot.active_perfectionism().is_some(), expected);
        assert_eq!(snapshot.challenges[0].is_available(), status == "available");
    }
}

#[test]
fn highest_prize_wins_with_first_entry_on_ties() {
    let snapshot = parse_challenges(&json!({"challenges": [
        challenge(1, "available", 2), challenge(2, "completed", 100),
        challenge(3, "available", 5), challenge(4, "available", 5)
    ]}))
    .unwrap();
    assert_eq!(snapshot.best_available().unwrap().id, 3);
    assert!(
        parse_challenges(&json!({"challenges": []}))
            .unwrap()
            .active()
            .is_none()
    );
}

#[test]
fn rejects_unknown_status_duplicates_and_multiple_active_challenges() {
    for status in ["paused", "", "expired", "IN_PROGRESS"] {
        assert!(parse_challenges(&json!({"challenges": [challenge(1, status, 5)]})).is_err());
    }
    for entries in [
        vec![challenge(1, "available", 1), challenge(1, "completed", 1)],
        vec![
            challenge(1, "in_progress", 1),
            challenge(2, "in_progress", 1),
        ],
    ] {
        assert!(parse_challenges(&json!({"challenges": entries})).is_err());
    }
}

#[test]
fn required_fields_are_not_defaulted_and_types_are_not_coerced() {
    for invalid in [
        json!({}),
        json!({"challenges": null}),
        json!({"challenges": {}}),
    ] {
        assert!(parse_challenges(&invalid).is_err());
    }
    let valid = challenge(1, "available", 5);
    for key in ["id", "title", "description", "prize", "status"] {
        let mut missing = valid.clone();
        missing.as_object_mut().unwrap().remove(key);
        assert!(
            parse_challenges(&json!({"challenges": [missing]})).is_err(),
            "{key}"
        );
    }
    for (key, value) in [
        ("id", json!(0)),
        ("id", json!(-1)),
        ("id", json!(1.5)),
        ("id", json!("1")),
        ("title", json!(" \u{a0}")),
        ("title", Value::Null),
        ("description", json!(4)),
        ("prize", json!(2147483648_i64)),
        ("prize", json!("5")),
        ("status", Value::Null),
    ] {
        let mut invalid = valid.clone();
        invalid[key] = value;
        assert!(
            parse_challenges(&json!({"challenges": [invalid]})).is_err(),
            "{key}"
        );
    }
    let mut nullable_description = valid;
    nullable_description["description"] = Value::Null;
    assert!(parse_challenges(&json!({"challenges": [nullable_description]})).is_ok());
}

#[test]
fn join_confirmation_requires_the_exact_chosen_active_id() {
    for status in ["available", "completed"] {
        let snapshot = parse_challenges(&json!({"challenges": [challenge(1, status, 5)]})).unwrap();
        assert!(confirm_challenge_join(&snapshot, 1).is_err());
    }
    let snapshot =
        parse_challenges(&json!({"challenges": [challenge(1, "in_progress", 5)]})).unwrap();
    assert!(confirm_challenge_join(&snapshot, 1).is_ok());
    assert!(confirm_challenge_join(&snapshot, 2).is_err());
}
