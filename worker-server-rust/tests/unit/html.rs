use super::*;
use serde_json::{Value, json};

#[test]
fn token_attributes_order_case_quotes_and_entities() {
    assert_eq!(
        csrf_token("<INPUT VALUE='a&amp;b&#x27;' NAME='_csrf_token'>").unwrap(),
        "a&b'"
    );
    assert!(csrf_token("<input name=_csrf_token value=' '>").is_err());
}

#[test]
fn page_entities_are_decoded_exactly_once() {
    let data = json!({"page":"wordset", "name":"Łódź &quot; < > '\""});
    let raw = data.to_string();
    let escaped = html_escape::encode_double_quoted_attribute(&raw);
    let html = format!("<DIV data-extra='x' DATA-PROPS=\"{escaped}\" ID='app'></DIV>");
    assert_eq!(page_props::<Value>(&html).unwrap(), data);
}

#[test]
fn missing_or_non_object_payload_stops() {
    for html in [
        "<html>login</html>",
        "<div id=app></div>",
        "<div id=app data-props='null'></div>",
        "<div id=app data-props='[]'></div>",
        "<div id=app data-props='{bad}'></div>",
    ] {
        assert!(page_props::<Value>(html).is_err());
    }
}

#[test]
fn login_form_is_classified_as_expired_authentication() {
    let error =
        page_props::<Value>("<form><input name=_csrf_token value=token></form>").unwrap_err();
    assert_eq!(error.kind, crate::error::ErrorKind::Auth);
}

#[test]
fn raw_text_and_template_contents_are_not_page_elements() {
    for tag in [
        "script", "style", "textarea", "template", "title", "xmp", "iframe", "noembed", "noframes",
        "noscript",
    ] {
        let html = format!(
            "<{tag}><input name=_csrf_token value=fake><div id=app data-props='{{\"fake\":true}}'></div></{tag}>"
        );
        assert!(csrf_token(&html).is_err(), "{tag}");
        assert_eq!(
            page_props::<Value>(&html).unwrap_err().kind,
            crate::error::ErrorKind::Parsing,
            "{tag}"
        );
        assert_eq!(
            csrf_token(&format!("{html}<input name=_csrf_token value=real>")).unwrap(),
            "real",
            "{tag}"
        );
        let real = format!("{html}<div id=app data-props='{{\"real\":true}}'></div>");
        assert_eq!(
            page_props::<Value>(&real).unwrap(),
            json!({"real":true}),
            "{tag}"
        );
    }
}

#[test]
fn raw_text_end_tags_are_delimited_and_case_insensitive() {
    for tag in ["script", "style", "textarea"] {
        let html = format!(
            "<{tag}/></{tag}-other><input name=_csrf_token value=fake></{} \t><input name=_csrf_token value=real>",
            tag.to_ascii_uppercase()
        );
        assert_eq!(csrf_token(&html).unwrap(), "real", "{tag}");
        assert!(csrf_token(&format!("<{tag}><input name=_csrf_token value=fake>")).is_err());
    }
}

#[test]
fn script_double_escaped_end_tag_is_still_text() {
    let html = "<script><!--<ScRiPt></script><input name=_csrf_token value=fake><div id=app data-props='{}'></div>--></script>";
    assert!(csrf_token(html).is_err());
    assert!(page_props::<Value>(html).is_err());
    assert_eq!(
        csrf_token(&format!("{html}<input name=_csrf_token value=real>")).unwrap(),
        "real"
    );
    assert_eq!(
        page_props::<Value>(&format!(
            "{html}<main id=app data-props='{{\"real\":true}}'></main>"
        ))
        .unwrap(),
        json!({"real":true})
    );
    assert_eq!(
        csrf_token("<script><!--><script></script><input name=_csrf_token value=real>").unwrap(),
        "real"
    );
}

#[test]
fn unbalanced_markup_in_script_does_not_swallow_real_elements() {
    let html = "<script>const markup = '<div><template>';</script>";
    assert_eq!(
        csrf_token(&format!("{html}<input name=_csrf_token value=real>")).unwrap(),
        "real"
    );
    assert_eq!(
        page_props::<Value>(&format!("{html}<main id=app data-props='{{}}'></main>")).unwrap(),
        json!({})
    );
}

#[test]
fn nested_templates_and_raw_text_do_not_leak_elements() {
    let html = "<template><template><input name=_csrf_token value=fake></template><textarea></template></textarea><script>'</template>'</script><div id=app data-props='{}'></div></template>";
    assert!(csrf_token(html).is_err());
    assert!(page_props::<Value>(html).is_err());
    assert_eq!(
        csrf_token(&format!("{html}<input name=_csrf_token value=real>")).unwrap(),
        "real"
    );
    assert_eq!(
        page_props::<Value>(&format!(
            "{html}<section id=app data-props='{{\"ok\":true}}'></section>"
        ))
        .unwrap(),
        json!({"ok":true})
    );
}

#[test]
fn comments_and_attribute_values_are_not_elements() {
    for end in ["-->", "--!>"] {
        let html = format!(
            "<!-- <input name=_csrf_token value=fake><div id=app data-props='{{}}'></div> {end}<input name=_csrf_token value=real>"
        );
        assert_eq!(csrf_token(&html).unwrap(), "real");
        assert!(page_props::<Value>(&html).is_err());
    }
    for comment in ["<!-->", "<!--->"] {
        assert_eq!(
            csrf_token(&format!("{comment}<input name=_csrf_token value=real>")).unwrap(),
            "real"
        );
    }
    let html = "<div title=\"<input name=_csrf_token value=fake><div id=app data-props='{}'>\"></div><input\tname = '_csrf_token'\r\nvalue = 'real>token'>";
    assert_eq!(csrf_token(html).unwrap(), "real>token");
    assert!(page_props::<Value>(html).is_err());
    assert!(csrf_token("<!-- <input name=_csrf_token value=fake>").is_err());
    assert!(csrf_token("</ <input name=_csrf_token value=fake>").is_err());
}

#[test]
fn empty_app_placeholders_do_not_hide_real_props_on_other_containers() {
    for tag in ["div", "main", "section", "MAIN", "SECTION"] {
        let html = format!(
            "<div id=app></div><main id=app data-props></main><section id=app data-props=' '></section><{tag} id=app data-props='{{\"ok\":true}}'></{tag}><div id=app></div>"
        );
        assert_eq!(page_props::<Value>(&html).unwrap(), json!({"ok":true}));
    }
}

#[test]
fn multiple_real_app_payloads_are_ambiguous_even_when_equal() {
    for second in [r#"{}"#, r#"{"other":true}"#, "invalid"] {
        let html = format!(
            "<div id=app data-props='{{}}'></div><main id=app data-props='{second}'></main>"
        );
        assert_eq!(
            page_props::<Value>(&html).unwrap_err().kind,
            crate::error::ErrorKind::Parsing
        );
    }
}

#[test]
fn authentication_takes_priority_over_any_app_payload_in_either_order() {
    for app in [
        "<div id=app></div>",
        "<div id=app data-props='invalid'></div>",
        "<div id=app data-props='[]'></div>",
        "<div id=app data-props='{}'></div>",
        "<div id=app data-props='{}'></div><section id=app data-props='{}'></section>",
    ] {
        let login = "<input name=_csrf_token>";
        for html in [format!("{app}{login}"), format!("{login}{app}")] {
            assert_eq!(
                page_props::<Value>(&html).unwrap_err().kind,
                crate::error::ErrorKind::Auth,
                "{app}"
            );
        }
    }
}

#[test]
fn unicode_entities_are_decoded_once_in_tokens_and_props() {
    assert_eq!(
        csrf_token(
            "<input name='_csrf_token' value='&#x0141;&#243;d&#x17a; &#x1F600; &copy; &amp;quot;'>"
        )
        .unwrap(),
        "Łódź 😀 © &quot;"
    );
    assert_eq!(page_props::<Value>("<main id='&#97;pp' data-props='{&quot;text&quot;:&quot;&#x0141;&#243;d&#x17a; &#x1F600; &copy; &amp;quot;&quot;}'></main>").unwrap(), json!({"text":"Łódź 😀 © &quot;"}));
    assert!(csrf_token("<input name='&amp;#95;csrf_token' value=fake>").is_err());
}

#[test]
fn first_duplicate_html_attribute_wins() {
    assert_eq!(
        csrf_token("<input name=_csrf_token NAME=other value=first VALUE=second>").unwrap(),
        "first"
    );
    assert!(csrf_token("<input name=other NAME=_csrf_token value=fake>").is_err());
    assert!(page_props::<Value>("<div id=other ID=app data-props='{}'></div>").is_err());
}

#[test]
fn page_json_duplicate_keys_are_rejected_before_typed_deserialization() {
    #[derive(Debug, serde::Deserialize)]
    struct Page {
        page: String,
    }
    let valid: Page =
        page_props("<main id=app data-props='{\"page\":\"dashboard\"}'></main>").unwrap();
    assert_eq!(valid.page, "dashboard");
    for props in [
        r#"{"page":"dashboard","page":"other"}"#,
        r#"{"page":"dashboard","ignored":{"x":1,"x":2}}"#,
        r#"{"page":"dashboard","ignored":[{"x":1,"\u0078":2}]}"#,
    ] {
        let html = format!("<main id=app data-props='{props}'></main>");
        assert!(page_props::<Value>(&html).is_err());
        assert!(page_props::<Page>(&html).is_err());
    }
}

#[test]
fn many_nodes_and_deep_nesting_need_no_dom_or_recursive_walk() {
    let mut html = "<div>".repeat(50_000);
    html.push_str(&"<!-- <div id=app data-props='{}'> -->".repeat(5_000));
    html.push_str(&"<template>".repeat(10_000));
    html.push_str("<input name=_csrf_token value=fake><div id=app data-props='{}'></div>");
    html.push_str(&"</template>".repeat(10_000));
    html.push_str("<section id=app data-props='{\"ok\":true}'></section>");
    html.push_str(&"</div>".repeat(50_000));
    assert_eq!(page_props::<Value>(&html).unwrap(), json!({"ok":true}));
    assert!(csrf_token(&html).is_err());
}

#[test]
fn html_input_is_size_bounded_even_without_session() {
    for prefix in [
        "<input name=_csrf_token value=token>",
        "<main id=app data-props='{}'></main>",
    ] {
        let mut html = String::with_capacity(MAX_HTML_BYTES + 1);
        html.push_str(prefix);
        html.extend(std::iter::repeat_n(' ', MAX_HTML_BYTES - prefix.len()));
        if prefix.starts_with("<input") {
            assert_eq!(csrf_token(&html).unwrap(), "token");
        } else {
            assert_eq!(page_props::<Value>(&html).unwrap(), json!({}));
        }
        html.push(' ');
        assert!(csrf_token(&html).is_err());
        assert!(page_props::<Value>(&html).is_err());
    }
}
