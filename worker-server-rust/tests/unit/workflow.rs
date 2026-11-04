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
    for invalid in [json!(1), json!(0), json!("2")] {
        page["wordset"]["flashcards"][1]["id"] = invalid;
        assert!(parse_wordset(&page, 1, &set()).is_err());
    }
    page["wordset"]["flashcards"][1]["id"] = Value::Null;
    assert!(parse_wordset(&page, 1, &set()).is_ok());
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
    let mut large_id = valid.clone();
    large_id["lessonId"] = json!(u64::MAX);
    assert_eq!(parse_learning_page(large_id).unwrap().lesson_id, u64::MAX);
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

#[test]
fn resolved_counts_match_individual_lookup_including_case_insensitive_group_keys() {
    let classes = [class("German", 1), class("gErMaN", 2), class("English", 3)];
    let config = Config {
        lesson_count: 4,
        class_lesson_counts: BTreeMap::from([
            ("GERMAN".into(), 8),
            ("GROUP:1".into(), 2),
            ("english".into(), 3),
        ]),
        ..Config::default()
    };
    let counts = resolve_counts(&config, &classes);
    assert_eq!(counts, [2, 4, 3]);
    for (class, count) in classes.iter().zip(counts) {
        assert_eq!(
            resolve_count(
                config.lesson_count,
                &config.class_lesson_counts,
                class,
                &classes
            ),
            count
        );
    }
}

#[test]
fn oversized_discovery_arrays_are_rejected_before_indexing_or_fetching() {
    let groups: Vec<_> = (1..=MAX_CLASSES + 1)
        .map(|id| json!({"id":id,"name":"A"}))
        .collect();
    assert!(parse_classes(&json!({"groups":groups})).is_err());
    let mut page = wordsets();
    page["wordsets"]["wordsets"] = json!(
        (1..=MAX_WORDSETS + 1)
            .map(|id| json!({"id":id,"name":"Set"}))
            .collect::<Vec<_>>()
    );
    assert!(parse_wordsets(&page, 1).is_err());
    let mut page = wordset();
    page["wordset"]["flashcards"] = json!(vec![
        json!({"word":"house","translation":"dom"});
        MAX_WORDSET_CARDS + 1
    ]);
    assert!(parse_wordset(&page, 1, &set()).is_err());
}

type MockResponse = (&'static str, String, Option<Value>, Value);

struct ScrapeMock {
    responses: std::cell::RefCell<std::collections::VecDeque<MockResponse>>,
}

impl ScrapeMock {
    fn new(responses: Vec<MockResponse>) -> Self {
        Self {
            responses: std::cell::RefCell::new(responses.into()),
        }
    }

    fn request(&self, method: &str, url: &str, body: Option<&Value>) -> Result<Value> {
        let (expected_method, expected_url, expected_body, response) = self
            .responses
            .borrow_mut()
            .pop_front()
            .expect("unexpected request");
        assert_eq!(
            (method, url, body),
            (
                expected_method,
                expected_url.as_str(),
                expected_body.as_ref()
            )
        );
        Ok(response)
    }

    fn done(&self) {
        assert!(self.responses.borrow().is_empty());
    }
}

impl ScrapeClient for ScrapeMock {
    fn get_page(&self, url: &str) -> Result<Value> {
        self.request("GET", url, None)
    }
    fn post(&self, url: &str, body: Option<&Value>) -> Result<Value> {
        self.request("POST", url, body)
    }
}

fn get(path: &str, response: Value) -> MockResponse {
    ("GET", path.into(), None, response)
}

#[test]
fn scrape_preserves_set_order_duplicates_and_empty_sets_despite_lesson_limit() {
    let mut page = dashboard();
    page["dashboard"]["lessonLimitReached"] = json!(true);
    let mut list = wordsets();
    list["wordsets"]["wordsets"] = json!([
        {"id":55,"name":"First","counts":{"total":2}},
        {"id":56,"name":"Empty","counts":{"total":0}},
        {"id":57,"name":"Last","counts":{"total":1}}
    ]);
    let mut first = wordset();
    first["wordset"]["flashcards"][1] = first["wordset"]["flashcards"][0].clone();
    let empty =
        json!({"page":"wordset","wordset":{"group":{"id":1},"wordset":{"id":56},"flashcards":[]}});
    let last = json!({"page":"wordset","wordset":{"group":{"id":1},"wordset":{"id":57},"flashcards":[{"word":"x","translation":"y"}]}});
    let mock = ScrapeMock::new(vec![
        get("/dashboard", page),
        get("/student/wordsets", list),
        get("/student/wordsets/55", first),
        get("/student/wordsets/56", empty),
        get("/student/wordsets/57", last),
    ]);
    let result = scrape_class_with(&mock, "/dashboard", 1).unwrap();
    mock.done();
    assert_eq!(
        result
            .wordsets
            .iter()
            .map(|set| set.info.id)
            .collect::<Vec<_>>(),
        [55, 56, 57]
    );
    assert_eq!(result.wordsets[0].entries[0], result.wordsets[0].entries[1]);
    assert!(result.wordsets[1].entries.is_empty());
    let json = serde_json::to_value(result).unwrap();
    assert_eq!(json["class"]["group_id"], "1");
    assert_eq!(json["wordsets"][0]["info"]["id"], "55");
}

#[test]
fn scrape_switch_is_confirmed_before_loading_sets_and_rejects_undiscovered_groups() {
    let mut switched = dashboard();
    switched["dashboard"]["currentGroup"]["id"] = json!(2);
    let mut list = wordsets();
    list["wordsets"]["currentGroup"]["id"] = json!(2);
    list["wordsets"]["wordsets"] = json!([]);
    let post = (
        "POST",
        "/student/context".into(),
        Some(json!({"kind":"group","id":2})),
        json!({}),
    );
    let mock = ScrapeMock::new(vec![
        get("/dashboard", dashboard()),
        post.clone(),
        get("/dashboard", switched),
        get("/student/wordsets", list),
    ]);
    assert_eq!(
        scrape_class_with(&mock, "/dashboard", 2)
            .unwrap()
            .class
            .group_id,
        2
    );
    mock.done();
    let mock = ScrapeMock::new(vec![
        get("/dashboard", dashboard()),
        post,
        get("/dashboard", dashboard()),
    ]);
    assert!(scrape_class_with(&mock, "/dashboard", 2).is_err());
    mock.done();
    let mock = ScrapeMock::new(vec![get("/dashboard", dashboard())]);
    assert!(scrape_class_with(&mock, "/dashboard", 3).is_err());
    mock.done();
    let mock = ScrapeMock::new(vec![]);
    assert!(scrape_class_with(&mock, "/dashboard", 0).is_err());
    mock.done();
}

#[test]
fn scrape_enforces_aggregate_card_and_text_budgets_including_duplicates_and_names() {
    let mut list = wordsets();
    list["wordsets"]["wordsets"] = json!([
        {"id":55,"name":"A"}, {"id":56,"name":"B"}
    ]);
    let many = vec![json!({"word":"a","translation":"b"}); MAX_EXPORT_CARDS];
    let detail = |id, cards| json!({"page":"wordset","wordset":{"group":{"id":1},"wordset":{"id":id},"flashcards":cards}});
    let mock = ScrapeMock::new(vec![
        get("/dashboard", dashboard()),
        get("/student/wordsets", list.clone()),
        get("/student/wordsets/55", detail(55, json!(many))),
        get(
            "/student/wordsets/56",
            detail(56, json!([{"word":"a","translation":"b"}])),
        ),
    ]);
    assert!(scrape_class_with(&mock, "/dashboard", 1).is_err());
    mock.done();

    // Terms fit individually, but repetitions and set/class names exceed 1 MiB.
    let term = "x".repeat(MAX_EXPORT_TERM_BYTES);
    let cards = vec![json!({"word":term,"translation":"y"}); 64];
    let mock = ScrapeMock::new(vec![
        get("/dashboard", dashboard()),
        get("/student/wordsets", list),
        get("/student/wordsets/55", detail(55, json!(cards))),
    ]);
    assert!(scrape_class_with(&mock, "/dashboard", 1).is_err());
    mock.done();
    let mut total = MAX_EXPORT_TEXT_BYTES - 1;
    export_text(&mut total, "A").unwrap();
    assert!(export_text(&mut total, "B").is_err());
    assert!(export_text(&mut 0, &"x".repeat(MAX_EXPORT_TERM_BYTES + 1)).is_err());

    // Names consume the same total budget, even before any detail request.
    let mut list = wordsets();
    list["wordsets"]["wordsets"] = json!(
        (1..=65)
            .map(|id| json!({"id":id,"name":"n".repeat(MAX_EXPORT_TERM_BYTES)}))
            .collect::<Vec<_>>()
    );
    let mock = ScrapeMock::new(vec![
        get("/dashboard", dashboard()),
        get("/student/wordsets", list),
    ]);
    assert!(scrape_class_with(&mock, "/dashboard", 1).is_err());
    mock.done();
}

#[test]
fn challenge_observation_is_marked_at_the_lesson_count_it_was_read() {
    let mut page = dashboard();
    page["dashboard"]["challenges"] = json!([
        {"id":9223372036854775807_i64,"title":" A ","description":" test   text ","prize":1,"status":"in_progress"},
        {"id":2,"title":"B","description":null,"prize":2,"status":"completed"},
        {"id":3,"title":"C","description":"available","prize":-1,"status":"available"}
    ]);
    let mut stats = RunStats::default();
    observe_dashboard(&mut stats, &parse_dashboard(&page, None).unwrap());
    stats.lessons_completed = 1;
    let snapshot = stats.challenge_snapshot.unwrap();
    assert_eq!(snapshot.lessons_completed_at_snapshot, 0);
    assert_eq!(
        (snapshot.available, snapshot.in_progress, snapshot.completed),
        (1, 1, 1)
    );
    let serialized = serde_json::to_value(&snapshot).unwrap();
    assert_eq!(serialized["active_id"], "9223372036854775807");
    assert_eq!(
        serialized["challenges"],
        json!([
            {"id":"9223372036854775807","title":"A","description":"test text","points":1,"status":"in_progress"},
            {"id":"2","title":"B","description":"","points":2,"status":"completed"},
            {"id":"3","title":"C","description":"available","points":-1,"status":"available"}
        ])
    );
}

#[test]
fn lesson_statistics_accumulate_without_partial_updates_on_overflow() {
    let mut stats = RunStats::default();
    let lesson = crate::lesson::LessonStats {
        words_learned: 4,
        errors_made: 2,
        ..Default::default()
    };
    record_lesson(&mut stats, &lesson).unwrap();
    record_lesson(&mut stats, &lesson).unwrap();
    assert_eq!(
        (
            stats.words_learned,
            stats.errors_made,
            stats.lessons_completed
        ),
        (8, 4, 2)
    );
    stats.errors_made = u64::MAX;
    let before = stats.clone();
    assert!(record_lesson(&mut stats, &lesson).is_err());
    assert_eq!(stats, before);
}

#[test]
fn latest_dashboard_replaces_challenge_items_and_snapshot_is_owned() {
    let mut page = dashboard();
    page["dashboard"]["challenges"] = json!([
        {"id":1,"title":"Old","description":"Old description","prize":5,"status":"in_progress"}
    ]);
    let mut stats = RunStats::default();
    let old = parse_dashboard(&page, None).unwrap();
    observe_dashboard(&mut stats, &old);
    let saved = stats.challenge_snapshot.clone().unwrap();
    stats.lessons_completed = 2;
    page["dashboard"]["currentGroup"]["id"] = json!(2);
    page["dashboard"]["challenges"] = json!([]);
    observe_dashboard(&mut stats, &parse_dashboard(&page, Some(2)).unwrap());
    let current = stats.challenge_snapshot.unwrap();
    assert_eq!(current.group_id, 2);
    assert_eq!(current.lessons_completed_at_snapshot, 2);
    assert!(current.challenges.is_empty());
    assert_eq!(current.active_id, None);
    assert_eq!(saved.challenges[0].title, "Old");
    assert_eq!(saved.active_id, Some(1));
}

#[test]
fn scrape_stops_on_invalid_set_without_requesting_further_details() {
    let mut list = wordsets();
    list["wordsets"]["wordsets"] = json!([
        {"id":55,"name":"First","counts":{"total":3}},
        {"id":56,"name":"Next"}
    ]);
    let mock = ScrapeMock::new(vec![
        get("/dashboard", dashboard()),
        get("/student/wordsets", list),
        get("/student/wordsets/55", wordset()),
    ]);
    assert!(scrape_class_with(&mock, "/dashboard", 1).is_err());
    mock.done();
}

#[test]
fn scrape_all_shares_card_text_and_wordset_budgets_between_classes() {
    for limit in ["cards", "text", "wordsets"] {
        let mut responses = vec![get("/dashboard", dashboard())];
        for group in 1..=2 {
            let mut page = dashboard();
            page["dashboard"]["currentGroup"]["id"] = json!(group);
            responses.push(get("/dashboard", page));
            let sets = if limit == "wordsets" && group == 1 {
                MAX_WORDSETS
            } else {
                1
            };
            responses.push(get("/student/wordsets", json!({"page":"wordsets","wordsets":{
                "currentGroup":{"id":group},
                "wordsets":(1..=sets).map(|id| json!({"id":id,"name":"Set"})).collect::<Vec<_>>()
            }})));
            if limit == "wordsets" && group == 2 {
                break;
            }
            for id in 1..=sets {
                let cards = match limit {
                    "cards" => vec![json!({"word":"a","translation":"b"}); 6000],
                    "text" => vec![
                        json!({"word":"x".repeat(MAX_EXPORT_TERM_BYTES),"translation":"y"});
                        32
                    ],
                    _ => vec![],
                };
                responses.push(get(
                    &format!("/student/wordsets/{id}"),
                    json!({"page":"wordset","wordset":{
                        "group":{"id":group},"wordset":{"id":id},"flashcards":cards
                    }}),
                ));
            }
        }
        let mock = ScrapeMock::new(responses);
        assert!(scrape_all_with(&mock, "/dashboard").is_err(), "{limit}");
        mock.done();
    }
}

#[test]
fn bounded_all_class_export_fits_runner_line_even_with_six_byte_json_escaping() {
    let mut budget = ScrapeBudget::default();
    let mut classes = Vec::new();
    let mut scrapes = Vec::new();
    for index in 0..MAX_CLASSES {
        let class = ClassInfo {
            group_id: i64::MAX - index as i64,
            title: "\u{1}".repeat(32),
        };
        // The class is serialized in both arrays.
        export_text(&mut budget.text_bytes, &class.title).unwrap();
        export_text(&mut budget.text_bytes, &class.title).unwrap();
        let info = WordsetInfo {
            id: i64::MAX,
            name: "\u{1}".repeat(32),
            expected_count: None,
        };
        export_text(&mut budget.text_bytes, &info.name).unwrap();
        let mut entries = Vec::new();
        for _ in 0..(MAX_EXPORT_CARDS / MAX_CLASSES
            + usize::from(index < MAX_EXPORT_CARDS % MAX_CLASSES))
        {
            let entry = VocabularyEntry {
                word: "\u{1}".repeat(50),
                translation: "\u{1}".repeat(50),
            };
            export_text(&mut budget.text_bytes, &entry.word).unwrap();
            export_text(&mut budget.text_bytes, &entry.translation).unwrap();
            entries.push(entry);
            budget.cards += 1;
        }
        classes.push(class.clone());
        scrapes.push(ScrapedClass {
            class,
            wordsets: vec![ScrapedWordset { info, entries }],
        });
    }
    assert_eq!(budget.cards, MAX_EXPORT_CARDS);
    let line = serde_json::to_vec(
        &json!({"type":"result","success":true,"classes":classes,"scrapes":scrapes,
        "duration_seconds":1.0,"api_calls":1000}),
    )
    .unwrap();
    assert!(line.len() + 1 < 16 * 1024 * 1024, "{} bytes", line.len());
}
