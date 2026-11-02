use super::*;

fn dashboard() -> Value {
    json!({"page": "dashboard", "dashboard": {
        "currentGroup": {"id": 1}, "contexts": {}, "lessonLimitReached": false,
        "groups": [{"id": 1, "name": "Class 1"}, {"id": 2, "name": "Class 2"}],
        "challenges": []
    }})
}

fn class(title: &str, id: i64) -> ClassInfo {
    ClassInfo {
        title: title.into(),
        group_id: id,
    }
}

fn wordsets() -> Value {
    json!({"page": "wordsets", "wordsets": {
        "currentGroup": {"id": 1},
        "wordsets": [{"id": 55, "name": " Set ", "counts": {"total": 2}}]
    }})
}

fn wordset() -> Value {
    json!({"page": "wordset", "wordset": {
        "group": {"id": 1}, "wordset": {"id": 55},
        "flashcards": [{"word": " house ", "translation": " dom "},
                       {"word": "home", "translation": "dom"}]
    }})
}

fn set() -> WordsetInfo {
    WordsetInfo {
        id: 55,
        name: "Set".into(),
        expected_count: Some(2),
    }
}

#[test]
fn dashboard_validates_page_group_limit_classes_and_challenges_together() {
    let parsed = parse_dashboard(&dashboard(), Some(1)).unwrap();
    assert_eq!(parsed.current_group_id, 1);
    assert!(!parsed.lesson_limit_reached);
    assert_eq!(parsed.classes.len(), 2);
    assert_eq!(parsed.classes[0].group_id, 1);
    assert!(parse_dashboard(&dashboard(), Some(2)).is_err());
    for (pointer, invalid) in [
        ("/page", json!("wordsets")),
        ("/dashboard", Value::Null),
        ("/dashboard/currentGroup", Value::Null),
        ("/dashboard/currentGroup/id", json!(0)),
        ("/dashboard/currentGroup/id", json!(3)),
        ("/dashboard/currentGroup/id", json!("1")),
        ("/dashboard/lessonLimitReached", json!("false")),
        ("/dashboard/challenges", Value::Null),
    ] {
        let mut page = dashboard();
        *page.pointer_mut(pointer).unwrap() = invalid;
        assert!(parse_dashboard(&page, None).is_err(), "{pointer}");
    }
    for field in ["currentGroup", "groups", "lessonLimitReached", "challenges"] {
        let mut page = dashboard();
        page["dashboard"].as_object_mut().unwrap().remove(field);
        assert!(parse_dashboard(&page, None).is_err(), "{field}");
    }
}

#[test]
fn rejects_all_malformed_class_discovery_cases_from_desktop() {
    let cases = [
        json!({"groups": []}),
        json!({}),
        json!({"groups": null}),
        json!({"groups": [{}]}),
        json!({"groups": [{"id": "1", "name": "A"}]}),
        json!({"groups": [{"id": 1, "name": null}]}),
        json!({"groups": [{"id": 1.5, "name": "A"}]}),
        json!({"groups": [{"id": 0, "name": "Class"}]}),
        json!({"groups": [{"id": 1, "name": " \u{a0}"}]}),
        json!({"groups": [{"id": 1, "name": "A"}, {"id": 1, "name": "B"}]}),
        json!({"groups": [{"id": 9223372036854775808_u64, "name": "A"}]}),
    ];
    for value in cases {
        assert!(parse_classes(&value).is_err(), "{value}");
    }
    let parsed = parse_classes(&json!({"groups": [
        {"id": 1, "name": " German\u{a0}  A "}, {"id": 2, "name": "German A"}
    ]}))
    .unwrap();
    assert_eq!(parsed[0].title, "German A");
    assert_eq!(parsed.len(), 2);
}

#[test]
fn switch_uses_context_api_or_absent_null_contexts_legacy_fallback() {
    let mut page = dashboard();
    let parsed = parse_dashboard(&page, None).unwrap();
    assert_eq!(
        group_switch_request(&parsed, 2).unwrap(),
        ("/student/context", json!({"kind": "group", "id": 2}))
    );
    assert!(group_switch_request(&parsed, 0).is_err());
    assert!(group_switch_request(&parsed, 3).is_err());
    page["dashboard"]
        .as_object_mut()
        .unwrap()
        .remove("contexts");
    let legacy = parse_dashboard(&page, None).unwrap();
    assert_eq!(
        group_switch_request(&legacy, 2).unwrap(),
        ("/student/group", json!({"groupId": 2}))
    );
    page["dashboard"]["contexts"] = Value::Null;
    assert!(!parse_dashboard(&page, None).unwrap().uses_context_api);
    for malformed in [json!([]), json!(false), json!("contexts")] {
        page["dashboard"]["contexts"] = malformed;
        assert!(parse_dashboard(&page, None).is_err());
    }
}

#[test]
fn daily_limit_is_a_valid_dashboard_and_a_special_error() {
    let mut page = dashboard();
    page["dashboard"]["lessonLimitReached"] = json!(true);
    let parsed = parse_dashboard(&page, None).unwrap();
    assert!(check_limit(&parsed).unwrap_err().is_limit());
    assert!(check_limit(&parse_dashboard(&dashboard(), None).unwrap()).is_ok());
}

#[test]
fn default_count_applies_to_every_class_independently() {
    let classes = [class("First", 1), class("Second", 2)];
    let counts = BTreeMap::new();
    for class in &classes {
        assert_eq!(resolve_count(4, &counts, class, &classes), 4);
    }
    let counts = BTreeMap::from([("group:1".into(), 0)]);
    assert_eq!(resolve_count(4, &counts, &classes[0], &classes), 0);
    assert_eq!(resolve_count(4, &counts, &classes[1], &classes), 4);
}

#[test]
fn stable_keys_win_and_legacy_titles_require_case_insensitive_uniqueness() {
    let classes = [class("German", 1), class("gErMaN", 2)];
    let counts = BTreeMap::from([("GERMAN".into(), 8), ("group:1".into(), 2)]);
    assert_eq!(resolve_count(4, &counts, &classes[0], &classes), 2);
    assert_eq!(resolve_count(4, &counts, &classes[1], &classes), 4);
    let unique = &classes[..1];
    assert_eq!(resolve_count(4, &counts, &unique[0], unique), 2);
    let legacy = BTreeMap::from([("GERMAN".into(), 8)]);
    assert_eq!(resolve_count(4, &legacy, &unique[0], unique), 8);
}

#[test]
fn vocabulary_preserves_all_candidates_and_normalizes_whitespace() {
    let list = parse_wordsets(&wordsets(), 1).unwrap();
    assert_eq!(list, vec![set()]);
    let cards = parse_wordset(&wordset(), 1, &list[0]).unwrap();
    assert_eq!(
        cards,
        vec![
            VocabularyEntry {
                word: "house".into(),
                translation: "dom".into()
            },
            VocabularyEntry {
                word: "home".into(),
                translation: "dom".into()
            },
        ]
    );
}

#[test]
fn vocabulary_list_requires_correct_page_group_ids_and_names() {
    assert!(parse_wordsets(&wordsets(), 2).is_err());
    for (pointer, value) in [
        ("/page", json!("dashboard")),
        ("/wordsets/currentGroup/id", json!("1")),
        ("/wordsets/wordsets", Value::Null),
        ("/wordsets/wordsets/0/id", json!(0)),
        ("/wordsets/wordsets/0/id", json!(1.5)),
        ("/wordsets/wordsets/0/name", json!(" ")),
        ("/wordsets/wordsets/0/name", Value::Null),
    ] {
        let mut page = wordsets();
        *page.pointer_mut(pointer).unwrap() = value;
        assert!(parse_wordsets(&page, 1).is_err(), "{pointer}");
    }
    let mut duplicate = wordsets();
    let entry = duplicate["wordsets"]["wordsets"][0].clone();
    duplicate["wordsets"]["wordsets"]
        .as_array_mut()
        .unwrap()
        .push(entry);
    assert!(parse_wordsets(&duplicate, 1).is_err());
}

#[test]
fn optional_counts_are_absent_not_null_or_coercible() {
    let mut page = wordsets();
    page["wordsets"]["wordsets"][0]
        .as_object_mut()
        .unwrap()
        .remove("counts");
    assert_eq!(parse_wordsets(&page, 1).unwrap()[0].expected_count, None);
    page["wordsets"]["wordsets"][0]["counts"] = json!({});
    assert_eq!(parse_wordsets(&page, 1).unwrap()[0].expected_count, None);
    for counts in [
        Value::Null,
        json!([]),
        json!({"total": null}),
        json!({"total": -1}),
        json!({"total": "2"}),
        json!({"total": 2.5}),
    ] {
        page["wordsets"]["wordsets"][0]["counts"] = counts;
        assert!(parse_wordsets(&page, 1).is_err());
    }
}

#[test]
fn vocabulary_details_require_exact_group_set_and_count() {
    assert!(parse_wordset(&wordset(), 2, &set()).is_err());
    for (pointer, value) in [
        ("/page", json!("wordsets")),
        ("/wordset/group/id", json!("1")),
        ("/wordset/wordset/id", json!(56)),
        ("/wordset/flashcards", Value::Null),
        ("/wordset/flashcards", json!([])),
    ] {
        let mut page = wordset();
        *page.pointer_mut(pointer).unwrap() = value;
        assert!(parse_wordset(&page, 1, &set()).is_err(), "{pointer}");
    }
    for count in [0, 1, 3] {
        let mut expected = set();
        expected.expected_count = Some(count);
        assert!(parse_wordset(&wordset(), 1, &expected).is_err());
    }
    let mut page = wordset();
    page["wordset"]["wordset"]["counts"] = json!({"total": 3});
    assert!(parse_wordset(&page, 1, &set()).is_err());
    page["wordset"]["wordset"]["counts"] = json!({"total": 2});
    page["wordset"]["counts"] = json!({"total": 1});
    assert!(parse_wordset(&page, 1, &set()).is_err());
}

#[test]
fn vocabulary_requires_every_nonempty_word_and_translation() {
    for field in ["word", "translation"] {
        for invalid in [Value::Null, json!(false), json!(" \u{a0} ")] {
            let mut page = wordset();
            page["wordset"]["flashcards"][0][field] = invalid;
            assert!(parse_wordset(&page, 1, &set()).is_err());
        }
        let mut page = wordset();
        page["wordset"]["flashcards"][0]
            .as_object_mut()
            .unwrap()
            .remove(field);
        assert!(parse_wordset(&page, 1, &set()).is_err());
    }
}

#[test]
fn optional_flashcard_ids_must_be_positive_and_unique_when_present() {
    let mut page = wordset();
    page["wordset"]["flashcards"][0]["id"] = json!(1);
    page["wordset"]["flashcards"][1]["id"] = json!(2);
    assert!(parse_wordset(&page, 1, &set()).is_ok());
    for invalid in [json!(1), json!(0), json!("2"), Value::Null] {
        page["wordset"]["flashcards"][1]["id"] = invalid;
        assert!(parse_wordset(&page, 1, &set()).is_err());
    }
}

#[test]
fn empty_vocabulary_pages_parse_but_cannot_supply_lesson_answers() {
    let mut page = wordsets();
    page["wordsets"]["wordsets"] = json!([]);
    assert!(parse_wordsets(&page, 1).unwrap().is_empty());
    let mut page = wordset();
    page["wordset"]["flashcards"] = json!([]);
    let mut expected = set();
    expected.expected_count = Some(0);
    assert!(parse_wordset(&page, 1, &expected).unwrap().is_empty());
}

#[test]
fn learning_page_limit_is_recognized_before_required_lesson_fields() {
    let error = parse_learning_page(json!({"reason": "limit-exceeded"})).unwrap_err();
    assert!(error.is_limit());
    let valid = json!({"status": "ok", "lessonId": 12, "step": {"state": "finished"}});
    assert!(parse_learning_page(valid.clone()).is_ok());
    let mut nullable_reason = valid.clone();
    nullable_reason["reason"] = Value::Null;
    assert!(parse_learning_page(nullable_reason).is_ok());
    for field in ["status", "lessonId", "step"] {
        let mut page = valid.clone();
        page.as_object_mut().unwrap().remove(field);
        assert!(parse_learning_page(page).is_err());
    }
    for (field, value) in [
        ("status", json!("error")),
        ("lessonId", json!(0)),
        ("lessonId", json!("12")),
        ("step", Value::Null),
        ("step", json!([])),
        ("reason", json!(false)),
    ] {
        let mut page = valid.clone();
        page[field] = value;
        assert!(parse_learning_page(page).is_err(), "{field}");
    }
}
