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
