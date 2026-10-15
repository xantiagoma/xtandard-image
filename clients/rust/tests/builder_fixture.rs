//! testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables.
//!
//! The URLs were written by the TypeScript client and parsed back by the Go
//! dialect tests (`TestBuilderFixture`); every client must write the same
//! ones: `url` with option names as given, `url_short` with the default short
//! names, and `query_url` (query dialect) where the case has one. Both the
//! fluent chains and the object form ([`Options`]) are checked.

mod common;

use serde_json::Value;
use xtandard_image::{
    Arg, EncryptionKey, ImgproxyUrlBuilder, InfoUrlParams, OptionNames, Options, QueryUrlBuilder, QueryUrlParams,
    SigningKey, SourceEncoding, UrlParams, VideoUrlParams,
};

use common::{arg, load_json};

type Pipeline = Vec<(String, Vec<Arg>)>;

fn pipelines(c: &Value) -> Vec<Pipeline> {
    c["pipelines"]
        .as_array()
        .expect("pipelines")
        .iter()
        .map(|pl| {
            pl.as_array()
                .expect("a pipeline")
                .iter()
                .flat_map(|entry| entry.as_object().expect("an entry").iter())
                .map(|(name, args)| (name.clone(), args.as_array().expect("args").iter().map(arg).collect()))
                .collect()
        })
        .collect()
}

/// Whether a pipeline repeats an option name: an `Options` (a mapping) cannot express that.
fn repeats(pls: &[Pipeline]) -> bool {
    pls.iter().any(|pl| pl.iter().enumerate().any(|(i, (n, _))| pl[..i].iter().any(|(m, _)| m == n)))
}

fn signing(fx: &Value, c: &Value) -> Option<SigningKey> {
    let size = c.get("signature_size").and_then(Value::as_u64).filter(|&s| s > 0)?;
    let key = SigningKey::new(fx["signing"]["key"].as_str()?, fx["signing"]["salt"].as_str()?).expect("key");
    Some(key.with_size(u8::try_from(size).expect("small")).expect("size"))
}

fn builder(fx: &Value, c: &Value, style: OptionNames) -> ImgproxyUrlBuilder {
    let enc = &fx["encryption"];
    let mut b = ImgproxyUrlBuilder::new()
        .unsigned_signature("unsafe")
        .encryption(
            EncryptionKey::new(enc["key"].as_str().expect("key"))
                .and_then(|k| k.with_iv(enc["iv"].as_str().expect("iv")))
                .expect("encryption key"),
        )
        .option_names(style);
    if let Some(k) = signing(fx, c) {
        b = b.signing(k);
    }
    if let Some(sep) = c.get("separator").and_then(Value::as_str) {
        let mut chars = sep.chars();
        b = b.separator(chars.next().expect("a separator"));
        assert!(chars.next().is_none(), "one-character separators only");
    }
    b
}

fn fluent(fx: &Value, c: &Value, style: OptionNames) -> String {
    let b = builder(fx, c, style);
    let source = c["source"].as_str().expect("source");
    let encoding: SourceEncoding = c["encoding"].as_str().expect("encoding").parse().expect("an encoding");
    let ext = c.get("extension").and_then(Value::as_str).filter(|e| !e.is_empty());
    let pls = pipelines(c);
    let built = match c.get("endpoint").and_then(Value::as_str) {
        Some("info") => {
            let mut chain = b.info(source).encoding(encoding);
            for (name, args) in pls.into_iter().flatten() {
                chain = chain.option(&name, args);
            }
            assert!(ext.is_none(), "/info URLs have no extension");
            chain.build()
        }
        Some("video") => {
            let mut chain = b.video(source).encoding(encoding);
            for (name, args) in pls.into_iter().flatten() {
                chain = chain.option(&name, args);
            }
            if let Some(e) = ext {
                chain = chain.extension(e);
            }
            chain.build()
        }
        _ => {
            let mut chain = b.image(source).encoding(encoding);
            for (i, pl) in pls.into_iter().enumerate() {
                if i > 0 {
                    chain = chain.pipeline();
                }
                for (name, args) in pl {
                    chain = chain.option(&name, args);
                }
            }
            if let Some(e) = ext {
                chain = chain.extension(e);
            }
            chain.build()
        }
    };
    built.unwrap_or_else(|e| panic!("{}: {e} ({})", c["name"], e.code()))
}

fn object(fx: &Value, c: &Value, style: OptionNames) -> String {
    let b = builder(fx, c, style);
    let source = c["source"].as_str().expect("source");
    let encoding: SourceEncoding = c["encoding"].as_str().expect("encoding").parse().expect("an encoding");
    let ext = c.get("extension").and_then(Value::as_str).filter(|e| !e.is_empty());
    let opts: Vec<Options> = pipelines(c).into_iter().map(|pl| pl.into_iter().collect()).collect();
    let built = match c.get("endpoint").and_then(Value::as_str) {
        Some("info") => b.info_url(source, opts, InfoUrlParams::new().source_encoding(encoding)),
        Some("video") => {
            let mut p = VideoUrlParams::new().source_encoding(encoding);
            if let Some(e) = ext {
                p = p.extension(e);
            }
            b.video_url(source, opts, p)
        }
        _ => {
            let mut p = UrlParams::new().source_encoding(encoding);
            if let Some(e) = ext {
                p = p.extension(e);
            }
            b.url(source, opts, p)
        }
    };
    built.unwrap_or_else(|e| panic!("{}: {e} ({})", c["name"], e.code()))
}

fn query(fx: &Value, c: &Value) -> String {
    let mut q = QueryUrlBuilder::new();
    if let Some(k) = signing(fx, c) {
        q = q.signing(k);
    }
    let source = c["source"].as_str().expect("source");
    let ext = c.get("extension").and_then(Value::as_str).filter(|e| !e.is_empty());
    let pls = pipelines(c);
    let built = if c.get("endpoint").and_then(Value::as_str) == Some("video") {
        let mut chain = q.video(source);
        for (name, args) in pls.into_iter().flatten() {
            chain = chain.option(&name, args);
        }
        if let Some(e) = ext {
            chain = chain.extension(e);
        }
        chain.build()
    } else {
        let mut chain = q.image(source);
        for (i, pl) in pls.iter().enumerate() {
            if i > 0 {
                chain = chain.pipeline();
            }
            for (name, args) in pl {
                chain = chain.option(name, args.clone());
            }
        }
        if let Some(e) = ext {
            chain = chain.extension(e);
        }
        let fluent = chain.build();
        // The object form must agree when it can express the case.
        if repeats(&pls) {
            return fluent.unwrap_or_else(|e| panic!("{}: {e} ({})", c["name"], e.code()));
        }
        let opts: Vec<Options> = pls.into_iter().map(|pl| pl.into_iter().collect()).collect();
        let mut p = QueryUrlParams::new();
        if let Some(e) = ext {
            p = p.extension(e);
        }
        assert_eq!(q.url(source, opts, p).ok(), fluent.clone().ok(), "{}", c["name"]);
        fluent
    };
    built.unwrap_or_else(|e| panic!("{}: {e} ({})", c["name"], e.code()))
}

#[test]
fn builder_fixture() {
    let fx = load_json("testdata/urls/imgproxy-builder.json");
    let cases = fx["cases"].as_array().expect("cases");
    assert!(cases.len() >= 400);
    let (mut urls, mut objects, mut queries) = (0, 0, 0);
    let mut failures = Vec::new();
    for c in cases {
        let name = c["name"].as_str().expect("name");
        let mut checks = vec![
            ("url (given, fluent)", fluent(&fx, c, OptionNames::Given), &c["url"]),
            ("url_short (fluent)", fluent(&fx, c, OptionNames::Short), &c["url_short"]),
        ];
        if !repeats(&pipelines(c)) {
            checks.push(("url (given, object)", object(&fx, c, OptionNames::Given), &c["url"]));
            checks.push(("url_short (object)", object(&fx, c, OptionNames::Short), &c["url_short"]));
            objects += 1;
        }
        for (label, got, want) in checks {
            if want.as_str() != Some(got.as_str()) {
                failures.push(format!("{name} {label}: want {want}, got {got}"));
            }
        }
        urls += 1;
        if c.get("query").and_then(Value::as_bool) == Some(true) {
            let got = query(&fx, c);
            if c["query_url"].as_str() != Some(got.as_str()) {
                failures.push(format!("{name} query_url: want {}, got {got}", c["query_url"]));
            }
            queries += 1;
        }
    }
    println!(
        "builder fixture: {urls} cases (url and url_short through the fluent form; {objects} also through the object form, \
         the rest repeat an option name), {queries} query URLs"
    );
    assert!(failures.is_empty(), "{} failures:\n{}", failures.len(), failures.join("\n"));
}
