//! testdata/security/vectors.json: the signing, `enc/` and `pdf_password` vectors shared with Go and TypeScript.

mod common;

use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use cbc::cipher::block_padding::Pkcs7;
use cbc::cipher::{BlockModeDecrypt, KeyIvInit};
use serde_json::Value;
use xtandard_image::{
    EncryptionKey, SigningKey, encrypt_pdf_password, encrypt_source, sign, sign_imgproxy_path, sign_xi_query,
    sign_xi_url, xi_message,
};

use common::load_json;

fn s<'a>(v: &'a Value, k: &str) -> &'a str {
    v[k].as_str().unwrap_or_else(|| panic!("{k} is not a string"))
}

fn hex(s: &str) -> Vec<u8> {
    (0..s.len()).step_by(2).map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("hex")).collect()
}

fn size(v: &Value) -> u8 {
    u8::try_from(v["size"].as_u64().expect("size")).expect("small")
}

fn decrypt(token: &str, key_hex: &str) -> String {
    let raw = URL_SAFE_NO_PAD.decode(token).expect("base64url");
    let (iv, ct) = raw.split_at(16);
    let key = hex(key_hex);
    let pt = match key.len() {
        16 => cbc::Decryptor::<aes::Aes128>::new_from_slices(&key, iv).expect("key").decrypt_padded_vec::<Pkcs7>(ct),
        24 => cbc::Decryptor::<aes::Aes192>::new_from_slices(&key, iv).expect("key").decrypt_padded_vec::<Pkcs7>(ct),
        _ => cbc::Decryptor::<aes::Aes256>::new_from_slices(&key, iv).expect("key").decrypt_padded_vec::<Pkcs7>(ct),
    };
    String::from_utf8(pt.expect("padding")).expect("UTF-8")
}

fn vectors(kind: &str) -> Vec<Value> {
    load_json("testdata/security/vectors.json")[kind].as_array().expect("vectors").clone()
}

#[test]
fn imgproxy_signatures() {
    let vs = vectors("imgproxy");
    assert!(vs.len() >= 3, "{}", vs.len());
    for v in &vs {
        let key = SigningKey::new(s(v, "key"), s(v, "salt")).and_then(|k| k.with_size(size(v))).expect("key");
        assert_eq!(sign(s(v, "path"), &key), s(v, "signature"), "{}", v["name"]);
        let path = s(v, "path");
        let p = if path.starts_with('/') { path.to_owned() } else { format!("/{path}") };
        assert_eq!(sign_imgproxy_path(path, &key), format!("/{}{p}", s(v, "signature")));
        let raw = SigningKey::new(hex(s(v, "key")), hex(s(v, "salt"))).and_then(|k| k.with_size(size(v))).expect("key");
        assert_eq!(sign(path, &raw), s(v, "signature"), "{} (raw bytes)", v["name"]);
    }
    println!("imgproxy vectors: {}", vs.len());
}

#[test]
fn xi_sig() {
    let vs = vectors("xi_sig");
    assert!(vs.len() >= 3, "{}", vs.len());
    for v in &vs {
        let key = SigningKey::new(s(v, "key"), s(v, "salt")).and_then(|k| k.with_size(size(v))).expect("key");
        let (path, query, signed) = (s(v, "path"), s(v, "query"), s(v, "signed_query"));
        assert_eq!(sign_xi_query(path, query, &key).expect("sign"), signed, "{}", v["name"]);
        assert_eq!(sign_xi_query(path, signed, &key).expect("re-sign"), signed, "{} (re-signed)", v["name"]);
        assert_eq!(
            sign_xi_url(&format!("https://img.example.com{path}?{query}"), &key, "").expect("sign url"),
            format!("https://img.example.com{path}?{signed}")
        );
    }
    println!("xi_sig vectors: {}", vs.len());
}

#[test]
fn encryption() {
    let vs = vectors("encryption");
    assert!(vs.len() >= 3, "{}", vs.len());
    for v in &vs {
        let key = EncryptionKey::new(s(v, "key")).and_then(|k| k.with_iv(s(v, "iv"))).expect("key");
        let token = encrypt_source(s(v, "plaintext"), &key).expect("encrypt");
        assert_eq!(token, s(v, "token"), "{}", v["name"]);
        assert_eq!(decrypt(&token, s(v, "key")), s(v, "plaintext"));
    }
    println!("encryption vectors: {}", vs.len());
}

#[test]
fn pdf_password() {
    let vs = vectors("pdf_password");
    assert!(vs.len() >= 3, "{}", vs.len());
    for v in &vs {
        let key = EncryptionKey::new(s(v, "key")).and_then(|k| k.with_iv(s(v, "iv"))).expect("key");
        assert_eq!(encrypt_pdf_password(s(v, "password"), &key).expect("encrypt"), s(v, "token"), "{}", v["name"]);
    }
    println!("pdf_password vectors: {}", vs.len());
}

#[test]
fn random_iv() {
    let key_hex = s(&vectors("pdf_password")[0], "key").to_owned();
    let key = EncryptionKey::new(key_hex.as_str()).expect("key");
    let a = encrypt_pdf_password("s3cret", &key).expect("a");
    let b = encrypt_pdf_password("s3cret", &key).expect("b");
    assert_ne!(a, b);
    assert_eq!(decrypt(&a, &key_hex), "s3cret");
    assert_eq!(decrypt(&b, &key_hex), "s3cret");
}

#[test]
fn xi_message_splits_the_signature() {
    assert_eq!(xi_message("/", "url=x&&xi_sig=s&w=1").expect("ok"), (Some("s".to_owned()), "/?url=x&w=1".to_owned()));
    assert_eq!(xi_message("/", "").expect("ok"), (None, "/".to_owned()));
    assert_eq!(xi_message("/", "xi_sig=a&xi_sig=b").expect_err("two").code(), "invalid_request");
}

#[test]
fn keys_are_not_printed() {
    let k = SigningKey::new("00".repeat(32), "11".repeat(32)).expect("key");
    let printed = format!("{k:?}");
    assert!(!printed.contains("00") && !printed.contains("11"), "{printed}");
    let e = EncryptionKey::new("22".repeat(32)).and_then(|k| k.with_iv("33".repeat(16))).expect("key");
    let printed = format!("{e:?}");
    assert!(!printed.contains("22") && !printed.contains("33") && !printed.contains("34"), "{printed}");
}
