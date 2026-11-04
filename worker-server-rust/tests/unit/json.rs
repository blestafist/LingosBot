use super::{MAX_DEPTH, MAX_NODES, parse};
use crate::error::ErrorKind;
use serde_json::json;

#[test]
fn preserves_json_types_and_distinct_keys() {
    let raw = r#"{"null":null,"bool":false,"min":-9223372036854775808,"max":18446744073709551615,"float":1.25,"text":"Zażółć & <> ","array":[{},[],true],"x":1,"X":2}"#;
    assert_eq!(
        parse(raw).unwrap(),
        serde_json::from_str::<serde_json::Value>(raw).unwrap()
    );
    assert_eq!(
        parse(r#"[{"x":1},{"x":2}]"#).unwrap(),
        json!([{"x":1},{"x":2}])
    );
}

#[test]
fn rejects_all_duplicate_keys_including_escaped_and_unknown_fields() {
    for raw in [
        r#"{"x":1,"x":2}"#,
        r#"{"x":1,"\u0078":2}"#,
        r#"{"unknown":{"x":1,"x":2}}"#,
        r#"{"unknown":[[],{"x":1,"x":2}]}"#,
        r#"{"a":{},"b":[{"secret":1,"secret":2}]}"#,
    ] {
        let error = parse(raw).unwrap_err();
        assert_eq!(error.kind, ErrorKind::Parsing);
        assert!(!error.message.contains("secret"));
    }
}

#[test]
fn bounds_array_and_object_depth_and_safe_partial_cleanup() {
    for depth in [MAX_DEPTH - 1, MAX_DEPTH, MAX_DEPTH + 1, 4096] {
        for object in [false, true] {
            let raw = if object {
                format!("{}0{}", "{\"x\":".repeat(depth), "}".repeat(depth))
            } else {
                format!("{}0{}", "[".repeat(depth), "]".repeat(depth))
            };
            assert_eq!(parse(&raw).is_ok(), depth <= MAX_DEPTH);
        }
    }
    let deep = format!(
        "{}0{}",
        "[".repeat(MAX_DEPTH - 1),
        "]".repeat(MAX_DEPTH - 1)
    );
    assert!(parse(&format!(r#"{{"retained":{deep},"x":0,"x":1}}"#)).is_err());
    assert!(parse(&format!(r#"{{"retained":{deep}}} trailing-secret"#)).is_err());
}

#[test]
fn bounds_node_count_for_both_wide_arrays_and_objects() {
    for count in [MAX_NODES - 1, MAX_NODES, MAX_NODES + 1] {
        let array = format!("[{}]", vec!["0"; count - 1].join(","));
        assert_eq!(parse(&array).is_ok(), count <= MAX_NODES);
        let entries: Vec<_> = (0..count - 1).map(|i| format!("\"{i}\":0")).collect();
        let object = format!("{{{}}}", entries.join(","));
        assert_eq!(parse(&object).is_ok(), count <= MAX_NODES);
    }
    let raw = format!(r#"{{"unknown":[{}]}}"#, vec!["{}"; MAX_NODES].join(","));
    assert!(parse(&raw).is_err());
}

#[test]
fn rejects_invalid_json_and_trailing_data_without_exposing_input() {
    for raw in [
        "",
        "{} {}",
        "{} secret",
        "[1,]",
        "{\"x\":}",
        "{\"x\":NaN}",
        "{\"x\":1e9999}",
        "{\"x\":\"\\uD800\"}",
    ] {
        let error = parse(raw).unwrap_err();
        assert_eq!(error.kind, ErrorKind::Parsing);
        assert!(!error.message.contains("secret"));
    }
}
