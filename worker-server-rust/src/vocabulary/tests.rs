use super::*;

fn answers(vocabulary: &Vocabulary, prompt: &str) -> Vec<String> {
    vocabulary.resolve(prompt).unwrap().to_vec()
}

#[test]
fn new_and_default_are_empty_and_unknown_prompts_do_not_resolve() {
    for vocabulary in [Vocabulary::new(), Vocabulary::default()] {
        assert!(vocabulary.is_empty());
        assert_eq!(vocabulary.len(), 0);
        assert!(vocabulary.resolve("unknown").is_none());
        assert!(vocabulary.resolve(" \t\u{a0}").is_none());
    }
}

#[test]
fn normalizes_whitespace_in_both_directions_and_preserves_spelling() {
    let mut vocabulary = Vocabulary::new();
    vocabulary
        .insert("  Good\u{a0}  Morning\n", " dzień\t dobry ")
        .unwrap();
    assert_eq!(answers(&vocabulary, "DZIEŃ\n DOBRY"), ["Good Morning"]);
    assert_eq!(answers(&vocabulary, " good\tMORNING "), ["dzień dobry"]);
    assert_eq!(vocabulary.len(), 1);
    assert!(!vocabulary.is_empty());
}

#[test]
fn candidates_are_ordered_and_case_insensitive_duplicates_are_removed() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("House", "dom").unwrap();
    vocabulary.insert("home", " DOM ").unwrap();
    vocabulary.insert(" HOUSE ", "\tdom\n").unwrap();
    assert_eq!(answers(&vocabulary, "dom"), ["House", "home"]);
    assert_eq!(answers(&vocabulary, "house"), ["dom"]);
    assert_eq!(vocabulary.len(), 1);
}

#[test]
fn reverse_lookup_keeps_multiple_translations_in_collection_order() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("castle", "zamek").unwrap();
    vocabulary.insert("CASTLE", "pałac").unwrap();
    assert_eq!(answers(&vocabulary, "Castle"), ["zamek", "pałac"]);
    assert_eq!(vocabulary.len(), 2);
}

#[test]
fn native_prompt_takes_precedence_over_a_reverse_collision() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("gift", "prezent").unwrap();
    vocabulary.insert("poison", "Gift").unwrap();
    assert_eq!(answers(&vocabulary, "gift"), ["poison"]);
}

#[test]
fn unicode_reverse_lookup_is_case_insensitive_without_dropping_accents() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("ŁÓDŹ", "船").unwrap();
    vocabulary.insert("Σ", "sigma").unwrap();
    vocabulary.insert("Straße", "ulica").unwrap();
    vocabulary.insert("Café", "kawiarnia").unwrap();
    assert_eq!(answers(&vocabulary, "\u{2003}łódź\u{a0}"), ["船"]);
    assert_eq!(answers(&vocabulary, "ς"), ["sigma"]);
    assert_eq!(answers(&vocabulary, "STRASSE"), ["ulica"]);
    assert_eq!(answers(&vocabulary, "CAFE\u{301}"), ["kawiarnia"]);
    assert!(vocabulary.resolve("lodz").is_none());
}

#[test]
fn blank_insertions_fail_without_partially_mutating_the_dictionary() {
    let mut vocabulary = Vocabulary::new();
    for (word, translation) in [("", "dom"), ("house", " \t\u{a0}"), ("\n", "")] {
        assert!(vocabulary.insert(word, translation).is_err());
        assert!(vocabulary.is_empty());
    }
    vocabulary.insert("house", "dom").unwrap();
    assert!(vocabulary.insert("bad", "\u{2003}").is_err());
    assert_eq!(vocabulary.len(), 1);
    assert!(vocabulary.resolve("bad").is_none());
}
