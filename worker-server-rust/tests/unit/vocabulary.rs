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
    assert_eq!(answers(&vocabulary, "STRAẞE"), ["ulica"]);
    assert!(vocabulary.resolve("STRASSE").is_none());
    assert_eq!(answers(&vocabulary, "CAFE\u{301}"), ["kawiarnia"]);
    assert!(vocabulary.resolve("lodz").is_none());
}

#[test]
fn expanding_case_mappings_do_not_remove_distinct_answer_candidates() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("Maße", "rozmiar").unwrap();
    vocabulary.insert("Masse", "rozmiar").unwrap();
    vocabulary.insert("MAẞE", "rozmiar").unwrap();
    assert_eq!(answers(&vocabulary, "ROZMIAR"), ["Maße", "Masse"]);

    vocabulary.insert("ﬃ", "litery").unwrap();
    vocabulary.insert("ffi", "litery").unwrap();
    assert_eq!(answers(&vocabulary, "litery"), ["ﬃ", "ffi"]);
}

#[test]
fn expanding_case_mappings_do_not_merge_different_reverse_prompts() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("Maße", "wymiary").unwrap();
    vocabulary.insert("Masse", "masa").unwrap();
    vocabulary.insert("ﬃ", "ligatura").unwrap();
    vocabulary.insert("ffi", "litery").unwrap();
    vocabulary.insert("İ", "kropka").unwrap();
    vocabulary.insert("i", "litera").unwrap();
    for (prompt, expected) in [
        ("maße", "wymiary"),
        ("MAẞE", "wymiary"),
        ("MASSE", "masa"),
        ("ﬃ", "ligatura"),
        ("FFI", "litery"),
        ("İ", "kropka"),
        ("I", "litera"),
    ] {
        assert_eq!(answers(&vocabulary, prompt), [expected], "{prompt}");
    }
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

#[test]
fn default_simple_folding_preserves_turkish_i_distinctions_without_expansion() {
    let mut vocabulary = Vocabulary::new();
    for word in ["I", "i", "ı", "İ", "I\u{307}", "i\u{307}"] {
        vocabulary.insert(word, "letter").unwrap();
    }
    // Default (non-Turkic) simple folding maps I to i, but does not expand İ
    // into i + dot or incorrectly uppercase dotless ı into I.
    assert_eq!(answers(&vocabulary, "letter"), ["I", "ı", "İ", "i\u{307}"]);
    assert_eq!(normalized_key("I"), normalized_key("i"));
    assert_eq!(normalized_key("I\u{307}"), normalized_key("İ"));
    for (left, right) in [("ı", "i"), ("İ", "i"), ("İ", "i\u{307}")] {
        assert_ne!(normalized_key(left), normalized_key(right));
    }

    vocabulary.insert("sık", "frequent").unwrap();
    vocabulary.insert("sik", "different word").unwrap();
    assert_eq!(answers(&vocabulary, "sık"), ["frequent"]);
    assert_eq!(answers(&vocabulary, "SIK"), ["different word"]);
}

#[test]
fn simple_folding_handles_single_character_equivalents_without_expansion() {
    for (left, right) in [
        ("Σ", "ς"),
        ("µ", "Μ"),
        ("ſ", "S"),
        ("K", "k"),
        ("ẞ", "ß"),
        ("Ꭰ", "ꭰ"),
        ("\u{1fd3}", "\u{390}"),
        ("\u{1fe3}", "\u{3b0}"),
        ("ﬅ", "ﬆ"),
    ] {
        assert_eq!(
            normalized_key(left),
            normalized_key(right),
            "{left}/{right}"
        );
    }
    for (left, right) in [("ß", "SS"), ("ﬃ", "ffi"), ("ﬅ", "st"), ("é", "e")] {
        assert_ne!(
            normalized_key(left),
            normalized_key(right),
            "{left}/{right}"
        );
    }
}

#[test]
fn nfd_duplicates_in_both_directions_keep_the_first_original_spelling() {
    let mut vocabulary = Vocabulary::new();
    vocabulary
        .insert("Cafe\u{301}", "re\u{301}sume\u{301}")
        .unwrap();
    let bytes = vocabulary.text_bytes;
    vocabulary.insert("CAFÉ", "RÉSUMÉ").unwrap();
    assert_eq!(answers(&vocabulary, "résumé"), ["Cafe\u{301}"]);
    assert_eq!(answers(&vocabulary, "café"), ["re\u{301}sume\u{301}"]);
    assert_eq!(vocabulary.entries.len(), 1);
    assert_eq!(vocabulary.text_bytes, bytes);
}

#[test]
fn ambiguous_direct_candidates_take_precedence_without_reverse_fallback() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("gift", "present").unwrap();
    vocabulary.insert("GIFT", "talent").unwrap();
    vocabulary.insert("poison", "Gift").unwrap();
    vocabulary.insert("venom", "gift").unwrap();
    assert_eq!(answers(&vocabulary, "GIFT"), ["poison", "venom"]);
    assert_eq!(answers(&vocabulary, "present"), ["gift"]);
    assert_eq!(answers(&vocabulary, "talent"), ["GIFT"]);
}

#[test]
fn punctuation_brackets_and_language_joiners_remain_significant() {
    let mut vocabulary = Vocabulary::new();
    let words = [
        "bank",
        "[bank]",
        "can't",
        "cant",
        "mother-in-law",
        "mother in law",
        "می\u{200c}روم",
        "میروم",
        "क्\u{200d}ष",
        "क्ष",
    ];
    for word in words {
        vocabulary.insert(word, "meaning").unwrap();
    }
    assert_eq!(answers(&vocabulary, "meaning"), words);
    for word in words {
        assert_eq!(answers(&vocabulary, word), ["meaning"]);
    }
    for unknown in ["(bank)", "bank!", "cantt", "motherinlaw"] {
        assert!(vocabulary.resolve(unknown).is_none(), "{unknown}");
    }
}

#[test]
fn comma_separated_meanings_are_kept_verbatim_not_guessed_as_synonyms() {
    let mut vocabulary = Vocabulary::new();
    vocabulary
        .insert("bank, shore (river)", "brzeg, wybrzeże")
        .unwrap();
    assert_eq!(
        answers(&vocabulary, "brzeg, wybrzeże"),
        ["bank, shore (river)"]
    );
    assert_eq!(
        answers(&vocabulary, "bank, shore (river)"),
        ["brzeg, wybrzeże"]
    );
    for unknown in ["bank", "shore", "brzeg", "wybrzeże"] {
        assert!(vocabulary.resolve(unknown).is_none(), "{unknown}");
    }
}

#[test]
fn streaming_whitespace_normalization_keeps_all_non_whitespace_characters() {
    assert_eq!(normalize_whitespace(" \t\r\n\u{a0}\u{2003}"), "");
    assert_eq!(normalize_whitespace("\t a \n b\u{a0}c\u{2003}"), "a b c");
    assert_eq!(
        normalize_whitespace("a\u{200c}\u{200d}b"),
        "a\u{200c}\u{200d}b"
    );
}

#[test]
fn oversized_input_is_a_parsing_error_without_partial_mutation() {
    let mut vocabulary = Vocabulary::new();
    vocabulary.insert("house", "dom").unwrap();
    let too_long = "x".repeat(MAX_TERM_BYTES + 1);
    let padded = format!("{}house", " ".repeat(MAX_TERM_BYTES));
    let bytes = vocabulary.text_bytes;
    for (word, translation) in [
        (too_long.as_str(), "dom"),
        ("house", too_long.as_str()),
        (padded.as_str(), "dom"),
    ] {
        let error = vocabulary.insert(word, translation).unwrap_err();
        assert_eq!(error.kind, crate::error::ErrorKind::Parsing);
        assert!(!error.message.contains(&too_long));
        assert_eq!(vocabulary.entries.len(), 1);
        assert_eq!(vocabulary.text_bytes, bytes);
        assert_eq!(answers(&vocabulary, "dom"), ["house"]);
        assert_eq!(answers(&vocabulary, "house"), ["dom"]);
    }
    assert!(vocabulary.resolve(&too_long).is_none());
    assert!(vocabulary.resolve(&padded).is_none());
}

#[test]
fn per_string_limit_accepts_the_boundary_and_counts_utf8_bytes() {
    let mut vocabulary = Vocabulary::new();
    let ascii = "x".repeat(MAX_TERM_BYTES);
    let unicode = "é".repeat(MAX_TERM_BYTES / 2);
    vocabulary.insert(&ascii, "ascii").unwrap();
    vocabulary.insert(&unicode, "unicode").unwrap();
    assert_eq!(vocabulary.resolve("ascii").unwrap()[0], ascii);
    assert_eq!(vocabulary.resolve("unicode").unwrap()[0], unicode);
    assert_eq!(answers(&vocabulary, &ascii), ["ascii"]);
    assert_eq!(answers(&vocabulary, &unicode), ["unicode"]);
    assert!(vocabulary.insert(&(unicode + "é"), "too long").is_err());
    assert!(vocabulary.resolve("too long").is_none());
}

#[test]
fn canonical_normalization_cannot_bypass_the_key_length_limit() {
    // U+0344 decomposes into two combining marks even under NFC.
    let expanded = "\u{344}".repeat(MAX_TERM_BYTES / 2);
    assert_eq!(expanded.len(), MAX_TERM_BYTES);
    let mut vocabulary = Vocabulary::new();
    for (word, translation) in [(expanded.as_str(), "marks"), ("marks", expanded.as_str())] {
        let error = vocabulary.insert(word, translation).unwrap_err();
        assert_eq!(error.kind, crate::error::ErrorKind::Parsing);
        assert!(vocabulary.is_empty());
        assert!(vocabulary.reverse.is_empty());
        assert!(vocabulary.entries.is_empty());
        assert_eq!(vocabulary.text_bytes, 0);
    }
}

#[test]
fn unique_entry_limit_also_bounds_many_candidates_for_one_ambiguous_prompt() {
    let mut vocabulary = Vocabulary::new();
    for index in 0..MAX_ENTRIES {
        vocabulary
            .insert(&format!("word-{index}"), "target")
            .unwrap();
    }
    assert_eq!(vocabulary.len(), 1);
    assert_eq!(vocabulary.resolve("target").unwrap().len(), MAX_ENTRIES);
    assert_eq!(vocabulary.resolve("target").unwrap()[0], "word-0");
    assert_eq!(
        vocabulary.resolve("target").unwrap()[MAX_ENTRIES - 1],
        format!("word-{}", MAX_ENTRIES - 1)
    );
    let bytes = vocabulary.text_bytes;
    for _ in 0..20 {
        vocabulary.insert(" WORD-0 ", "TARGET").unwrap();
    }
    assert_eq!(vocabulary.text_bytes, bytes);
    assert_eq!(vocabulary.entries.len(), MAX_ENTRIES);
    assert_eq!(vocabulary.text_bytes, stored_text_bytes(&vocabulary));
    let error = vocabulary.insert("overflow", "target").unwrap_err();
    assert_eq!(error.kind, crate::error::ErrorKind::Parsing);
    assert!(vocabulary.resolve("overflow").is_none());
    assert_eq!(vocabulary.resolve("target").unwrap().len(), MAX_ENTRIES);
    assert_eq!(vocabulary.text_bytes, bytes);
}

#[test]
fn unique_entry_limit_bounds_distinct_prompts_and_reverse_ambiguity_too() {
    for shared_word in [false, true] {
        let mut vocabulary = Vocabulary::new();
        for index in 0..MAX_ENTRIES {
            let word = if shared_word {
                "word".to_owned()
            } else {
                format!("w{index}")
            };
            vocabulary.insert(&word, &format!("t{index}")).unwrap();
        }
        assert_eq!(vocabulary.len(), MAX_ENTRIES);
        let bytes = vocabulary.text_bytes;
        assert!(vocabulary.insert("new-word", "new-prompt").is_err());
        assert!(vocabulary.resolve("new-word").is_none());
        assert!(vocabulary.resolve("new-prompt").is_none());
        assert_eq!(vocabulary.entries.len(), MAX_ENTRIES);
        assert_eq!(vocabulary.text_bytes, bytes);
        assert_eq!(vocabulary.text_bytes, stored_text_bytes(&vocabulary));
        if shared_word {
            assert_eq!(vocabulary.resolve("word").unwrap().len(), MAX_ENTRIES);
        }
    }
}

fn stored_text_bytes(vocabulary: &Vocabulary) -> usize {
    let maps: usize = vocabulary
        .direct
        .iter()
        .chain(&vocabulary.reverse)
        .map(|(key, candidates)| key.len() + candidates.iter().map(String::len).sum::<usize>())
        .sum();
    maps + vocabulary
        .entries
        .iter()
        .map(|(word, translation)| word.len() + translation.len())
        .sum::<usize>()
}

#[test]
fn total_text_budget_includes_both_directions_and_the_deduplication_index() {
    let mut vocabulary = Vocabulary::new();
    let suffix = "x".repeat(1024);
    let mut rejected = None;
    for index in 0..MAX_ENTRIES {
        let word = format!("{index:05}{suffix}");
        let bytes = vocabulary.text_bytes;
        let count = vocabulary.entries.len();
        match vocabulary.insert(&word, "meaning") {
            Ok(()) => assert!(vocabulary.text_bytes <= MAX_TEXT_BYTES),
            Err(error) => {
                assert_eq!(error.kind, crate::error::ErrorKind::Parsing);
                assert_eq!(vocabulary.text_bytes, bytes);
                assert_eq!(vocabulary.entries.len(), count);
                rejected = Some(word);
                break;
            }
        }
    }
    let rejected = rejected.expect("text budget must be reached before the entry limit");
    assert!(vocabulary.resolve(&rejected).is_none());
    assert_eq!(
        vocabulary.resolve("meaning").unwrap().len(),
        vocabulary.entries.len()
    );
    assert_eq!(vocabulary.text_bytes, stored_text_bytes(&vocabulary));
    assert!(vocabulary.text_bytes > MAX_TEXT_BYTES - 4 * (suffix.len() + 32));

    let bytes = vocabulary.text_bytes;
    let first = vocabulary.resolve("meaning").unwrap()[0].to_uppercase();
    for _ in 0..20 {
        vocabulary.insert(&first, "MEANING").unwrap();
    }
    assert_eq!(vocabulary.text_bytes, bytes);
    assert_eq!(vocabulary.text_bytes, stored_text_bytes(&vocabulary));
    // Cloning must preserve budgeting, and a failed large insertion must not
    // prevent a smaller entry from using the remaining space.
    let mut cloned = vocabulary.clone();
    cloned.insert("a", "b").unwrap();
    assert_eq!(cloned.text_bytes, bytes + 6);
    assert_eq!(cloned.text_bytes, stored_text_bytes(&cloned));
    assert_eq!(vocabulary.text_bytes, bytes);
}
