//! clients/spec/conformance.json: the cases every official client runs (ADR 0013).
//!
//! Positive cases must produce `expect.value` byte for byte; error cases must
//! fail with `expect.error` as the error code. The fixture's options are
//! pipelines of `[name, args]` pairs; they are built twice here, as
//! [`Options`] (the object form) and through the fluent chains, and both
//! must agree. Cases the typed API cannot express are skipped with the reason.

mod common;

use serde_json::Value;
use xtandard_image::{
    Arg, ByteRange, EncryptionKey, Error, ImgproxyUrlBuilder, InfoUrlParams, OptionNames, Options, Params,
    QueryUrlBuilder, QueryUrlParams, SegmentFormat, SigningKey, SourceEncoding, SourceScheme, StreamParams,
    StreamProtocol, UrlParams, VideoUrlParams, encrypt_pdf_password, encrypt_source, escape_plain_source,
    escape_query_value, range_header, sign, sign_xi_url, source_url, xi_source,
};

use common::{arg, load_json};

/// Why a case cannot run against the typed API.
type Skip = &'static str;
type Outcome = Result<Result<String, Error>, Skip>;

struct Fixture {
    keys: Value,
}

fn s(v: &Value) -> &str {
    v.as_str().expect("a string")
}

impl Fixture {
    fn signing(&self, cfg: &Value) -> Result<Option<SigningKey>, Error> {
        let Some(sg) = cfg.get("signing") else { return Ok(None) };
        let k = if sg == &Value::Bool(true) { &self.keys["signing"] } else { sg };
        let mut key = SigningKey::new(s(&k["key"]), s(&k["salt"]))?;
        if let Some(size) = cfg.get("signature_size") {
            key = key.with_size(u8::try_from(size.as_u64().expect("a size")).expect("a small size"))?;
        }
        Ok(Some(key))
    }

    fn encryption(&self, cfg: &Value) -> Result<Option<EncryptionKey>, Error> {
        let Some(en) = cfg.get("encryption") else { return Ok(None) };
        let k = if en == &Value::Bool(true) { &self.keys["encryption"] } else { en };
        let mut key = EncryptionKey::new(s(&k["key"]))?;
        if let Some(iv) = k.get("iv") {
            key = key.with_iv(s(iv))?;
        }
        Ok(Some(key))
    }
}

fn separator(cfg: &Value) -> Result<Option<char>, Skip> {
    let Some(sep) = cfg.get("separator") else { return Ok(None) };
    let mut chars = s(sep).chars();
    match (chars.next(), chars.next()) {
        (Some(c), None) => Ok(Some(c)),
        _ => Err("not expressible: the separator is a char, so it cannot be empty or longer"),
    }
}

fn imgproxy(f: &Fixture, cfg: &Value) -> Result<Result<ImgproxyUrlBuilder, Error>, Skip> {
    let sep = separator(cfg)?;
    Ok((|| {
        let mut b = ImgproxyUrlBuilder::new();
        if let Some(k) = f.signing(cfg)? {
            b = b.signing(k);
        }
        if let Some(k) = f.encryption(cfg)? {
            b = b.encryption(k);
        }
        if let Some(v) = cfg.get("base_url") {
            b = b.base_url(s(v));
        }
        if let Some(v) = cfg.get("unsigned_signature") {
            b = b.unsigned_signature(s(v));
        }
        if let Some(v) = cfg.get("source_encoding") {
            b = b.source_encoding(s(v).parse()?);
        }
        if let Some(v) = cfg.get("split_source") {
            b = b.split_source(usize::try_from(v.as_u64().expect("a size")).expect("small"));
        }
        if let Some(c) = sep {
            b = b.separator(c);
        }
        if let Some(v) = cfg.get("option_names") {
            b = b.option_names(s(v).parse()?);
        }
        Ok(b)
    })())
}

fn query(f: &Fixture, cfg: &Value) -> Result<Result<QueryUrlBuilder, Error>, Skip> {
    let sep = separator(cfg)?;
    Ok((|| {
        let mut b = QueryUrlBuilder::new();
        if let Some(k) = f.signing(cfg)? {
            b = b.signing(k);
        }
        if let Some(k) = f.encryption(cfg)? {
            b = b.encryption(k);
        }
        if let Some(v) = cfg.get("base_url") {
            b = b.base_url(s(v));
        }
        if let Some(v) = cfg.get("mount") {
            b = b.mount(s(v));
        }
        if let Some(c) = sep {
            b = b.separator(c);
        }
        if let Some(v) = cfg.get("option_names") {
            b = b.option_names(s(v).parse::<OptionNames>()?);
        }
        Ok(b)
    })())
}

/// The fixture's pipelines: lists of `[name, [args…]]`.
fn pipelines(inp: &Value) -> Vec<Vec<(String, Vec<Arg>)>> {
    inp.get("options").and_then(Value::as_array).map_or_else(Vec::new, |pls| {
        pls.iter()
            .map(|pl| {
                pl.as_array()
                    .expect("a pipeline")
                    .iter()
                    .map(|e| (s(&e[0]).to_owned(), e[1].as_array().expect("args").iter().map(arg).collect()))
                    .collect()
            })
            .collect()
    })
}

/// The pipelines as `Options` (mapping form).
fn options(inp: &Value) -> Vec<Options> {
    pipelines(inp).into_iter().map(|pl| pl.into_iter().collect()).collect()
}

fn params(inp: &Value) -> Params {
    inp.get("params")
        .and_then(Value::as_array)
        .map_or_else(Params::new, |ps| ps.iter().map(|p| (s(&p[0]).to_owned(), arg(&p[1]))).collect())
}

fn opt_str<'a>(inp: &'a Value, key: &str) -> Option<&'a str> {
    inp.get(key).and_then(Value::as_str)
}

fn stream(inp: &Value) -> Result<StreamParams, Error> {
    let protocol: StreamProtocol = s(&inp["protocol"]).parse()?;
    let mut st = StreamParams::new(protocol);
    if let Some(r) = inp.get("renditions").and_then(Value::as_array) {
        st = st.renditions(r.iter().map(|v| u32::try_from(v.as_u64().expect("a rendition")).expect("small")));
    }
    if let Some(d) = inp.get("segment_duration") {
        st = st.segment_duration(u32::try_from(d.as_u64().expect("seconds")).expect("small"));
    }
    if let Some(f) = opt_str(inp, "segment_format") {
        st = st.segment_format(f.parse::<SegmentFormat>()?);
    }
    let mut opts = options(inp);
    assert!(opts.len() <= 1, "stream cases have one pipeline");
    if let Some(o) = opts.pop() {
        st = st.options(o);
    }
    if let Some(e) = opt_str(inp, "source_encoding") {
        st = st.source_encoding(e.parse()?);
    }
    Ok(st)
}

fn range(v: &Value) -> Result<ByteRange, Skip> {
    if let Some(raw) = v.as_str() {
        return Ok(raw.into());
    }
    let n = |k: &str| v.get(k).map(|x| x.as_u64().ok_or("not expressible: byte ranges are u64, never negative"));
    if let Some(suffix) = n("suffix") {
        return Ok(ByteRange::suffix(suffix?));
    }
    let start = n("start").expect("a start")?;
    Ok(match n("end") {
        Some(end) => ByteRange::new(start, end?),
        None => ByteRange::from_start(start),
    })
}

/// The case through the object form (`Options`, params structs, free functions).
fn run_mapping(f: &Fixture, case: &Value) -> Outcome {
    let empty = Value::Object(serde_json::Map::new());
    let cfg = case.get("config").unwrap_or(&empty);
    let inp = &case["input"];
    let source = || s(&inp["source"]);
    let encoding = || opt_str(inp, "source_encoding").map(str::parse::<SourceEncoding>).transpose();
    Ok(match s(&case["op"]) {
        "imgproxy.url" => imgproxy(f, cfg)?.and_then(|b| {
            let mut p = UrlParams::new();
            if let Some(e) = opt_str(inp, "extension") {
                p = p.extension(e);
            }
            if let Some(e) = encoding()? {
                p = p.source_encoding(e);
            }
            if let Some(pw) = opt_str(inp, "pdf_password") {
                p = p.pdf_password(pw);
            }
            b.url(source(), options(inp), p)
        }),
        "imgproxy.info_url" => imgproxy(f, cfg)?.and_then(|b| {
            let mut p = InfoUrlParams::new();
            if let Some(e) = encoding()? {
                p = p.source_encoding(e);
            }
            if let Some(pw) = opt_str(inp, "pdf_password") {
                p = p.pdf_password(pw);
            }
            b.info_url(source(), options(inp), p)
        }),
        "imgproxy.video_url" => {
            if inp.get("pdf_password").is_some() {
                return Err("not expressible: VideoUrlParams has no PDF password (the type system rejects it)");
            }
            imgproxy(f, cfg)?.and_then(|b| {
                let mut p = VideoUrlParams::new();
                if let Some(e) = opt_str(inp, "extension") {
                    p = p.extension(e);
                }
                if let Some(e) = encoding()? {
                    p = p.source_encoding(e);
                }
                b.video_url(source(), options(inp), p)
            })
        }
        "imgproxy.stream_url" => imgproxy(f, cfg)?.and_then(|b| b.stream_url(source(), &stream(inp)?)),
        "query.url" => query(f, cfg)?.and_then(|b| {
            let mut p = QueryUrlParams::new().params(params(inp));
            if let Some(e) = opt_str(inp, "extension") {
                p = p.extension(e);
            }
            if let Some(pw) = opt_str(inp, "pdf_password") {
                p = p.pdf_password(pw);
            }
            b.url(source(), options(inp), p)
        }),
        "query.video_url" => {
            query(f, cfg)?.and_then(|b| b.video_url(source(), options(inp), opt_str(inp, "extension")))
        }
        "query.stream_url" => query(f, cfg)?.and_then(|b| b.stream_url(source(), &stream(inp)?)),
        "query.imaginary_url" => {
            query(f, cfg)?.and_then(|b| b.imaginary_url(s(&inp["endpoint"]), source(), params(inp)))
        }
        "sign" => f.signing(cfg).map(|k| sign(s(&inp["message"]), &k.expect("a key"))),
        "sign_xi_url" => f
            .signing(cfg)
            .and_then(|k| sign_xi_url(s(&inp["url"]), &k.expect("a key"), opt_str(inp, "mount").unwrap_or(""))),
        "encrypt_source" => f.encryption(cfg).and_then(|k| encrypt_source(source(), &k.expect("a key"))),
        "encrypt_pdf_password" => {
            f.encryption(cfg).and_then(|k| encrypt_pdf_password(s(&inp["password"]), &k.expect("a key")))
        }
        "xi_source" => xi_source(s(&inp["name"]), s(&inp["key"])),
        "source_url" => s(&inp["scheme"])
            .parse::<SourceScheme>()
            .and_then(|scheme| source_url(scheme, s(&inp["authority"]), s(&inp["key"]))),
        "range_header" => range_header(&range(&inp["range"])?),
        "escape_plain_source" => Ok(escape_plain_source(s(&inp["value"]))),
        "escape_query_value" => Ok(escape_query_value(s(&inp["value"]))),
        op => panic!("unknown op {op}"),
    })
}

/// Applies the fixture's pipelines to a chain through `option()` (and `pipeline()` when it has one).
macro_rules! apply {
    ($chain:expr, $pls:expr, pipeline) => {{
        let mut c = $chain;
        for (i, pl) in $pls.into_iter().enumerate() {
            if i > 0 {
                c = c.pipeline();
            }
            for (name, args) in pl {
                c = c.option(&name, args);
            }
        }
        c
    }};
    ($chain:expr, $pls:expr) => {{
        let mut c = $chain;
        for pl in $pls {
            for (name, args) in pl {
                c = c.option(&name, args);
            }
        }
        c
    }};
}

/// The case through the fluent chains, for the ops that have one (`None` otherwise).
fn run_fluent(f: &Fixture, case: &Value) -> Option<Outcome> {
    let empty = Value::Object(serde_json::Map::new());
    let cfg = case.get("config").unwrap_or(&empty);
    let inp = &case["input"];
    let source = opt_str(inp, "source").unwrap_or_default();
    let pls = pipelines(inp);
    let chained = pls.len() > 1;
    let ext = opt_str(inp, "extension");
    let pdf = opt_str(inp, "pdf_password");
    let encoding = match opt_str(inp, "source_encoding").map(str::parse::<SourceEncoding>).transpose() {
        Ok(e) => e,
        Err(e) => return Some(Ok(Err(e))),
    };
    let out = match s(&case["op"]) {
        "imgproxy.url" => imgproxy(f, cfg).map(|b| {
            b.and_then(|b| {
                let mut c = apply!(b.image(source), pls, pipeline);
                if let Some(e) = encoding {
                    c = c.encoding(e);
                }
                if let Some(e) = ext {
                    c = c.extension(e);
                }
                if let Some(pw) = pdf {
                    c = c.with_pdf_password(pw);
                }
                c.build()
            })
        }),
        // /info and /video chains cannot chain pipelines; the mapping form covers that error.
        "imgproxy.info_url" if !chained => imgproxy(f, cfg).map(|b| {
            b.and_then(|b| {
                let mut c = apply!(b.info(source), pls);
                if let Some(e) = encoding {
                    c = c.encoding(e);
                }
                if let Some(pw) = pdf {
                    c = c.with_pdf_password(pw);
                }
                c.build()
            })
        }),
        "imgproxy.video_url" if !chained && pdf.is_none() => imgproxy(f, cfg).map(|b| {
            b.and_then(|b| {
                let mut c = apply!(b.video(source), pls);
                if let Some(e) = encoding {
                    c = c.encoding(e);
                }
                if let Some(e) = ext {
                    c = c.extension(e);
                }
                c.build()
            })
        }),
        "query.url" => query(f, cfg).map(|b| {
            b.and_then(|b| {
                let mut c = b.image(source);
                for (k, v) in params(inp).iter() {
                    c = c.param(k, v.clone());
                }
                let mut c = apply!(c, pls, pipeline);
                if let Some(e) = ext {
                    c = c.extension(e);
                }
                if let Some(pw) = pdf {
                    c = c.with_pdf_password(pw);
                }
                c.build()
            })
        }),
        "query.video_url" if !chained => query(f, cfg).map(|b| {
            b.and_then(|b| {
                let mut c = apply!(b.video(source), pls);
                if let Some(e) = ext {
                    c = c.extension(e);
                }
                c.build()
            })
        }),
        _ => return None,
    };
    Some(out)
}

#[test]
fn conformance_fixture() {
    let fixture = load_json("clients/spec/conformance.json");
    let f = Fixture { keys: fixture["keys"].clone() };
    let cases = fixture["cases"].as_array().expect("cases");
    assert!(cases.len() >= 100);
    let ops: std::collections::BTreeSet<&str> = cases.iter().map(|c| s(&c["op"])).collect();
    assert!(ops.len() >= 17, "{ops:?}");

    let (mut passed, mut fluent_checked, mut skipped, mut failures) = (0, 0, Vec::new(), Vec::new());
    for case in cases {
        let id = format!("{}:{}", s(&case["op"]), s(&case["name"]));
        let expect = &case["expect"];
        let got = match run_mapping(&f, case) {
            Ok(r) => r,
            Err(reason) => {
                skipped.push(format!("{id}: {reason}"));
                continue;
            }
        };
        let check =
            |label: &str, got: &Result<String, Error>, failures: &mut Vec<String>| match (expect.get("value"), got) {
                (Some(want), Ok(url)) if want.as_str() == Some(url) => {}
                (None, Err(e)) if expect["error"].as_str() == Some(e.code()) => {}
                _ => failures.push(format!("{id} ({label}): want {expect}, got {got:?}")),
            };
        check("object form", &got, &mut failures);
        if let Some(Ok(fluent)) = run_fluent(&f, case) {
            check("fluent form", &fluent, &mut failures);
            fluent_checked += 1;
        }
        passed += 1;
    }
    println!(
        "conformance: {passed} cases run ({fluent_checked} also through the fluent form), {} skipped",
        skipped.len()
    );
    for s in &skipped {
        println!("  skipped {s}");
    }
    assert!(failures.is_empty(), "{} failures:\n{}", failures.len(), failures.join("\n"));
}
