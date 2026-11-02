use crate::error::{Error, Result};
use serde::de::DeserializeOwned;
use std::borrow::Cow;

// Match the HTTP body limit, including callers which do not use Session.
const MAX_HTML_BYTES: usize = 1024 * 1024;
const MAX_NAMESPACE_SCOPES: usize = 64;

fn space(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\r' | b'\n' | 0x0c)
}

struct Tag<'a> {
    name: &'a str,
    attributes: &'a str,
}

impl<'a> Tag<'a> {
    fn is(&self, name: &str) -> bool {
        self.name.eq_ignore_ascii_case(name)
    }

    fn attribute(&self, name: &str) -> Option<Cow<'a, str>> {
        self.scan_attributes(Some(name)).0
    }

    fn self_closing(&self) -> bool {
        self.scan_attributes(None).1
    }

    fn scan_attributes(&self, wanted: Option<&str>) -> (Option<Cow<'a, str>>, bool) {
        let bytes = self.attributes.as_bytes();
        let mut pos = 0;
        while pos < bytes.len() {
            while pos < bytes.len() && space(bytes[pos]) {
                pos += 1;
            }
            if bytes.get(pos) == Some(&b'/') {
                pos += 1;
                if bytes[pos..].iter().all(|&byte| space(byte)) {
                    return (None, true);
                }
                continue;
            }
            let start = pos;
            while pos < bytes.len() && !space(bytes[pos]) && !matches!(bytes[pos], b'=' | b'/') {
                pos += 1;
            }
            let key = &self.attributes[start..pos];
            while pos < bytes.len() && space(bytes[pos]) {
                pos += 1;
            }
            let mut value = "";
            if bytes.get(pos) == Some(&b'=') {
                pos += 1;
                while pos < bytes.len() && space(bytes[pos]) {
                    pos += 1;
                }
                let quote = bytes
                    .get(pos)
                    .copied()
                    .filter(|b| matches!(b, b'\'' | b'"'));
                pos += usize::from(quote.is_some());
                let start = pos;
                while pos < bytes.len()
                    && !quote.map_or_else(|| space(bytes[pos]), |q| bytes[pos] == q)
                {
                    pos += 1;
                }
                value = &self.attributes[start..pos];
                pos += usize::from(quote.is_some() && pos < bytes.len());
            }
            // HTML keeps the first occurrence of a duplicate attribute.
            if wanted.is_some_and(|name| key.eq_ignore_ascii_case(name)) {
                return (Some(html_escape::decode_html_entities(value)), false);
            }
        }
        (None, false)
    }
}

#[derive(Clone, Copy, PartialEq)]
enum Namespace {
    Html,
    Svg,
    Math,
    MathText,
}

struct NamespaceScope<'a> {
    name: &'a str,
    children: Namespace,
}

/// A bounded, forward-only tag scanner, not a DOM or an HTML repair parser.
/// Only active opening tags matter. Raw/RCDATA bodies and nested templates are
/// skipped without allocating nodes. Only namespace boundaries need a small,
/// bounded stack; ordinary HTML nesting needs no stack or recursive walk.
struct Tags<'a> {
    html: &'a str,
    pos: usize,
    templates: usize,
    scopes: Vec<NamespaceScope<'a>>,
    scope_limit_reached: bool,
}

impl<'a> Tags<'a> {
    fn new(html: &'a str) -> Result<Self> {
        if html.len() > MAX_HTML_BYTES {
            return Err(Error::parsing("Page HTML exceeds the size limit."));
        }
        Ok(Self {
            html,
            pos: 0,
            templates: 0,
            scopes: Vec::new(),
            scope_limit_reached: false,
        })
    }

    fn namespace(&mut self, tag: &Tag<'a>, closing: bool) -> Namespace {
        if self.scopes.is_empty() && !tag.is("svg") && !tag.is("math") {
            return Namespace::Html;
        }
        let parent = self.scopes.last().map_or(Namespace::Html, |s| s.children);
        if closing {
            if let Some(index) = self.scopes.iter().rposition(|s| tag.is(s.name)) {
                self.scopes.truncate(index);
            }
            return parent;
        }

        // SVG integration points and MathML text/annotation integration points
        // put their HTML children back in HTML, even inside a foreign root.
        let html_child = parent == Namespace::Html
            || (parent == Namespace::MathText && !tag.is("mglyph") && !tag.is("malignmark"));
        let namespace = if html_child {
            if tag.is("svg") {
                Namespace::Svg
            } else if tag.is("math") {
                Namespace::Math
            } else {
                Namespace::Html
            }
        } else if parent == Namespace::MathText {
            Namespace::Math
        } else if parent == Namespace::Math
            && tag.is("svg")
            && self
                .scopes
                .last()
                .is_some_and(|s| s.name.eq_ignore_ascii_case("annotation-xml"))
        {
            Namespace::Svg
        } else {
            parent
        };
        let math_text = ["mi", "mo", "mn", "ms", "mtext"]
            .iter()
            .any(|name| tag.is(name));
        let svg_html = tag.is("foreignObject") || tag.is("desc");
        let annotation = tag.is("annotation-xml");
        let children = if (namespace == Namespace::Svg && svg_html)
            || (namespace == Namespace::Math
                && annotation
                && tag.attribute("encoding").is_some_and(|encoding| {
                    encoding.eq_ignore_ascii_case("text/html")
                        || encoding.eq_ignore_ascii_case("application/xhtml+xml")
                })) {
            Namespace::Html
        } else if namespace == Namespace::Math && math_text {
            Namespace::MathText
        } else {
            namespace
        };
        let boundary = tag.is("svg")
            || tag.is("math")
            || (!self.scopes.is_empty() && (svg_html || annotation || math_text))
            || parent == Namespace::MathText
            // Shadow any matching ancestor, not just the innermost boundary:
            // an inner HTML </span> must not pop an outer MathML span through
            // intervening SVG/foreignObject scopes.
            || self.scopes.iter().any(|s| tag.is(s.name));
        let html_void = boundary
            && namespace == Namespace::Html
            && [
                "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta",
                "param", "source", "track", "wbr",
            ]
            .iter()
            .any(|name| tag.is(name));
        if boundary && !html_void && (namespace == Namespace::Html || !tag.self_closing()) {
            if self.scopes.len() == MAX_NAMESPACE_SCOPES {
                self.scope_limit_reached = true;
                self.pos = self.html.len();
            } else {
                self.scopes.push(NamespaceScope {
                    name: tag.name,
                    children,
                });
            }
        }
        namespace
    }

    fn tag_end(&self, start: usize) -> Option<usize> {
        let mut quote = None;
        for (offset, &byte) in self.html.as_bytes()[start..].iter().enumerate() {
            match quote {
                Some(q) if byte == q => quote = None,
                None if matches!(byte, b'\'' | b'"') => quote = Some(byte),
                None if byte == b'>' => return Some(start + offset),
                _ => {}
            }
        }
        None
    }

    fn named_at(&self, pos: usize, prefix: &[u8], name: &str) -> bool {
        let bytes = self.html.as_bytes();
        let end = pos + prefix.len() + name.len();
        bytes[pos..].starts_with(prefix)
            && bytes
                .get(pos + prefix.len()..end)
                .is_some_and(|s| s.eq_ignore_ascii_case(name.as_bytes()))
            && bytes
                .get(end)
                .is_some_and(|&b| space(b) || matches!(b, b'/' | b'>'))
    }

    fn skip_comment(&mut self) {
        self.pos += 4; // <!--
        let bytes = self.html.as_bytes();
        // HTML also permits abrupt empty comment endings.
        if bytes[self.pos..].starts_with(b">") {
            self.pos += 1;
        } else if bytes[self.pos..].starts_with(b"->") {
            self.pos += 2;
        } else {
            while self.pos < bytes.len() {
                let rest = &bytes[self.pos..];
                if rest.starts_with(b"-->") {
                    self.pos += 3;
                    return;
                }
                if rest.starts_with(b"--!>") {
                    self.pos += 4;
                    return;
                }
                self.pos += 1;
            }
        }
    }

    fn skip_cdata(&mut self) {
        self.pos += 9; // <![CDATA[
        self.pos += self.html[self.pos..]
            .find("]]>")
            .map_or(self.html.len() - self.pos, |end| end + 3);
    }

    fn skip_raw(&mut self, name: &str, foreign: bool) {
        // Script's HTML comment escapes can double-escape a <script> opener.
        // In that state the first </script> is text, not the closing tag.
        let script = name.eq_ignore_ascii_case("script");
        let (mut escaped, mut double_escaped) = (false, false);
        let bytes = self.html.as_bytes();
        while self.pos < bytes.len() {
            if foreign && bytes[self.pos..].starts_with(b"<![CDATA[") {
                self.skip_cdata();
            } else if script && !escaped && bytes[self.pos..].starts_with(b"<!--") {
                escaped = true;
                self.pos += 2;
            } else if script && escaped && bytes[self.pos..].starts_with(b"-->") {
                (escaped, double_escaped) = (false, false);
                self.pos += 3;
            } else if bytes[self.pos] == b'<' && self.named_at(self.pos, b"</", name) {
                if !double_escaped {
                    return;
                }
                double_escaped = false;
                self.pos += 2 + name.len();
            } else if script
                && escaped
                && bytes[self.pos] == b'<'
                && self.named_at(self.pos, b"<", name)
            {
                double_escaped = true;
                self.pos += 1 + name.len();
            } else {
                self.pos += 1;
            }
        }
    }

    fn next(&mut self) -> Option<Tag<'a>> {
        let bytes = self.html.as_bytes();
        loop {
            self.pos += self.html[self.pos..].find('<')?;
            let start = self.pos;
            if bytes[start..].starts_with(b"<!--") {
                self.skip_comment();
                continue;
            }
            // SVG/MathML CDATA is inert text, including any literal '>' or tags.
            // Outside foreign content this is malformed HTML; still never use
            // its contents as trusted page data.
            if bytes[start..].starts_with(b"<![CDATA[") {
                self.skip_cdata();
                continue;
            }
            self.pos += 1;
            if matches!(bytes.get(self.pos), Some(b'!' | b'?')) {
                self.pos = self.tag_end(self.pos).map_or(bytes.len(), |end| end + 1);
                continue;
            }
            let closing = bytes.get(self.pos) == Some(&b'/');
            self.pos += usize::from(closing);
            if !bytes.get(self.pos).is_some_and(u8::is_ascii_alphabetic) {
                // An invalid end-tag opener becomes a bogus HTML comment.
                if closing {
                    self.pos += self.html[self.pos..]
                        .find('>')
                        .map_or(bytes.len() - self.pos, |end| end + 1);
                }
                continue;
            }
            let name_start = self.pos;
            while self.pos < bytes.len()
                && !space(bytes[self.pos])
                && !matches!(bytes[self.pos], b'/' | b'>')
            {
                self.pos += 1;
            }
            let name = &self.html[name_start..self.pos];
            let end = self.tag_end(self.pos)?;
            let tag = Tag {
                name,
                attributes: &self.html[self.pos..end],
            };
            self.pos = end + 1;
            let foreign = self.namespace(&tag, closing) != Namespace::Html;
            if self.scope_limit_reached {
                return None;
            }
            if tag.is("template") {
                if closing {
                    self.templates = self.templates.saturating_sub(1);
                } else {
                    self.templates += 1;
                }
            } else if !closing {
                if tag.is("plaintext") {
                    self.pos = bytes.len();
                } else if [
                    "script", "style", "textarea", "title", "xmp", "iframe", "noembed", "noframes",
                    "noscript",
                ]
                .iter()
                .any(|name| tag.is(name))
                {
                    // Foreign elements honor '/>', unlike HTML raw-text tags.
                    // In particular an SVG <title/> must not consume the page.
                    if !foreign || !tag.self_closing() {
                        self.skip_raw(name, foreign);
                    }
                } else if self.templates == 0 {
                    return Some(tag);
                }
            }
        }
    }
}

pub fn csrf_token(html: &str) -> Result<String> {
    let mut tags = Tags::new(html)?;
    while let Some(tag) = tags.next() {
        if tag.is("input")
            && tag.attribute("name").as_deref() == Some("_csrf_token")
            && let Some(value) = tag.attribute("value")
            && !value.trim().is_empty()
        {
            return Ok(value.into_owned());
        }
    }
    if tags.scope_limit_reached {
        return Err(Error::parsing(
            "HTML namespace nesting exceeds the safety limit.",
        ));
    }
    Err(Error::parsing(
        "Login page has no CSRF token; Lingos may have changed.",
    ))
}

/// Decode the attribute once, then deserialize inert JSON. No scripts execute.
pub fn page_props<T: DeserializeOwned>(html: &str) -> Result<T> {
    let mut tags = Tags::new(html)?;
    let mut login_form = false;
    let mut props = None;
    let mut ambiguous = false;
    while let Some(tag) = tags.next() {
        if tag.is("input") && tag.attribute("name").as_deref() == Some("_csrf_token") {
            login_form = true;
        }
        if (tag.is("div") || tag.is("main") || tag.is("section"))
            && tag.attribute("id").as_deref() == Some("app")
            && let Some(raw) = tag.attribute("data-props")
            && !raw.trim().is_empty()
        {
            if props.is_some() {
                ambiguous = true;
            } else {
                props = Some(raw);
            }
        }
    }
    if tags.scope_limit_reached {
        return Err(Error::parsing(
            "HTML namespace nesting exceeds the safety limit.",
        ));
    }
    if login_form {
        return Err(Error::auth(
            "Session expired: login form received instead of page data.",
        ));
    }
    if ambiguous {
        return Err(Error::parsing("Page contains ambiguous server data."));
    }
    let raw = props.ok_or_else(|| {
        Error::parsing("Page has no server data; session expired or Lingos changed.")
    })?;
    if !raw.trim_start().starts_with('{') {
        return Err(Error::parsing("Page server data is not an object."));
    }
    serde_json::from_value(super::json::parse(&raw)?)
        .map_err(|_| Error::parsing("Page contains invalid or changed server data."))
}

#[cfg(test)]
#[path = "../../tests/unit/html.rs"]
mod tests;
