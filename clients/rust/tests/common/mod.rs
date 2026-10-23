//! Helpers of the integration tests: the shared fixtures (read from the
//! repository by relative path) and a stand-in HTTP server.

#![allow(dead_code)]

pub mod standin;

use std::collections::HashMap;
use std::fmt::Write as _;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::thread;

use serde_json::Value;
use xtandard_image::Arg;

/// The repository root (clients/rust → ../..).
pub fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..")
}

/// A JSON file of the repository.
pub fn load_json(rel: &str) -> Value {
    let path = root().join(rel);
    let text = std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("{}: {e}", path.display()));
    serde_json::from_str(&text).unwrap_or_else(|e| panic!("{}: {e}", path.display()))
}

/// A fixture argument as an [`Arg`]: integers stay integers, other numbers are floats.
pub fn arg(v: &Value) -> Arg {
    match v {
        Value::Null => Arg::Null,
        Value::Bool(b) => Arg::Bool(*b),
        Value::Number(n) => n
            .as_i64()
            .map(Arg::from)
            .or_else(|| n.as_u64().map(Arg::from))
            .unwrap_or_else(|| Arg::Float(n.as_f64().expect("a JSON number"))),
        Value::String(s) => Arg::Str(s.clone()),
        other => panic!("unsupported argument {other}"),
    }
}

/// A request the stand-in server received.
#[derive(Debug, Clone)]
pub struct Seen {
    pub method: String,
    /// Path and query, as sent.
    pub target: String,
    /// Lower-case header names.
    pub headers: HashMap<String, String>,
    /// The request content (`Content-Length` bytes), as text.
    pub body: String,
}

/// A response of the stand-in server.
pub struct Reply {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
}

impl Reply {
    pub fn new(status: u16, content_type: &str, body: impl Into<Vec<u8>>) -> Self {
        Self { status, headers: vec![("Content-Type".into(), content_type.into())], body: body.into() }
    }

    pub fn json(status: u16, body: &Value) -> Self {
        Self::new(status, "application/json", body.to_string())
    }

    pub fn header(mut self, k: &str, v: &str) -> Self {
        self.headers.push((k.into(), v.into()));
        self
    }
}

/// A stand-in HTTP/1.1 server on 127.0.0.1 (one thread, `Connection: close`).
pub struct Server {
    pub base: String,
    pub seen: Arc<Mutex<Vec<Seen>>>,
}

impl Server {
    pub fn start(handler: impl Fn(&Seen) -> Reply + Send + 'static) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
        let base = format!("http://{}", listener.local_addr().expect("addr"));
        let seen = Arc::new(Mutex::new(Vec::new()));
        let log = Arc::clone(&seen);
        thread::spawn(move || {
            for stream in listener.incoming() {
                let Ok(stream) = stream else { continue };
                if let Some(req) = serve(stream, &handler) {
                    log.lock().expect("lock").push(req);
                }
            }
        });
        Self { base, seen }
    }

    pub fn last(&self) -> Seen {
        self.seen.lock().expect("lock").last().cloned().expect("a request")
    }
}

fn serve(stream: TcpStream, handler: &impl Fn(&Seen) -> Reply) -> Option<Seen> {
    let mut reader = BufReader::new(stream.try_clone().ok()?);
    let mut line = String::new();
    reader.read_line(&mut line).ok()?;
    let mut parts = line.split_whitespace();
    let method = parts.next()?.to_owned();
    let target = parts.next()?.to_owned();
    let mut headers = HashMap::new();
    loop {
        let mut h = String::new();
        reader.read_line(&mut h).ok()?;
        let h = h.trim_end();
        if h.is_empty() {
            break;
        }
        if let Some((k, v)) = h.split_once(':') {
            headers.insert(k.trim().to_ascii_lowercase(), v.trim().to_owned());
        }
    }
    let mut body = Vec::new();
    if let Some(n) = headers.get("content-length").and_then(|v| v.parse::<usize>().ok()) {
        body = vec![0; n];
        reader.read_exact(&mut body).ok()?;
    }
    let req = Seen { method, target, headers, body: String::from_utf8_lossy(&body).into_owned() };
    let reply = handler(&req);
    let mut out =
        format!("HTTP/1.1 {} X\r\nConnection: close\r\nContent-Length: {}\r\n", reply.status, reply.body.len());
    for (k, v) in &reply.headers {
        let _ = write!(out, "{k}: {v}\r\n");
    }
    out.push_str("\r\n");
    let mut stream = stream;
    stream.write_all(out.as_bytes()).ok()?;
    if req.method != "HEAD" {
        stream.write_all(&reply.body).ok()?;
    }
    stream.flush().ok()?;
    Some(req)
}

/// A base URL nothing listens on.
pub fn closed_base() -> String {
    let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
    let addr = listener.local_addr().expect("addr");
    drop(listener);
    format!("http://{addr}")
}
