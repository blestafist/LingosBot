use crate::error::{Error, Result};
use serde::de::DeserializeOwned;

fn attribute(tag: &tl::HTMLTag<'_>, name: &str) -> Option<String> {
    tag.attributes().iter().find_map(|(key, value)| {
        key.eq_ignore_ascii_case(name)
            .then(|| value.map(|v| html_escape::decode_html_entities(&v).into_owned()))
            .flatten()
    })
}

pub fn csrf_token(html: &str) -> Result<String> {
    let dom = tl::parse(html, tl::ParserOptions::default())
        .map_err(|_| Error::parsing("Could not parse login HTML."))?;
    for node in dom.nodes() {
        let Some(tag) = node.as_tag() else { continue };
        if tag.name().as_bytes().eq_ignore_ascii_case(b"input")
            && attribute(tag, "name").as_deref() == Some("_csrf_token")
            && let Some(value) = attribute(tag, "value")
            && !value.trim().is_empty()
        {
            return Ok(value);
        }
    }
    Err(Error::parsing(
        "Login page has no CSRF token; Lingos may have changed.",
    ))
}

/// Decode the attribute once, then deserialize inert JSON. No scripts execute.
pub fn page_props<T: DeserializeOwned>(html: &str) -> Result<T> {
    let dom = tl::parse(html, tl::ParserOptions::default())
        .map_err(|_| Error::parsing("Could not parse page HTML."))?;
    let mut login_form = false;
    for node in dom.nodes() {
        let Some(tag) = node.as_tag() else { continue };
        if tag.name().as_bytes().eq_ignore_ascii_case(b"input")
            && attribute(tag, "name").as_deref() == Some("_csrf_token")
        {
            login_form = true;
        }
        if tag.name().as_bytes().eq_ignore_ascii_case(b"div")
            && attribute(tag, "id").as_deref() == Some("app")
        {
            let raw = attribute(tag, "data-props")
                .ok_or_else(|| Error::parsing("Page has no server data."))?;
            if !raw.trim_start().starts_with('{') {
                return Err(Error::parsing("Page server data is not an object."));
            }
            return serde_json::from_str(&raw)
                .map_err(|_| Error::parsing("Page contains invalid or changed server data."));
        }
    }
    if login_form {
        Err(Error::auth(
            "Session expired: login form received instead of page data.",
        ))
    } else {
        Err(Error::parsing(
            "Page has no server data; session expired or Lingos changed.",
        ))
    }
}

#[cfg(test)]
mod tests;
