//! Option catalogues at run time and their serialisation into option segments.

use crate::args::{Arg, Entry};
use crate::error::{Error, Result};
use crate::text::{format_float, format_int};

/// How option names are written.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default)]
pub enum OptionNames {
    /// The shortest alias (`rs`, `q`): the default.
    #[default]
    Short,
    /// The canonical name (`resize`, `quality`).
    Canonical,
    /// The name as the caller wrote it (a name or an alias).
    Given,
}

impl std::str::FromStr for OptionNames {
    type Err = Error;

    fn from_str(s: &str) -> Result<Self> {
        match s {
            "short" => Ok(Self::Short),
            "canonical" => Ok(Self::Canonical),
            "given" => Ok(Self::Given),
            _ => Err(Error::InvalidRequest(format!("unknown option name style {s:?}"))),
        }
    }
}

/// One option catalogue: every name and alias to its canonical name, and canonical to short names.
#[derive(Debug, Clone, Copy)]
pub(crate) struct Catalogue {
    pub(crate) kind: &'static str,
    /// Sorted by name.
    pub(crate) names: &'static [(&'static str, &'static str)],
    /// Sorted by canonical name.
    pub(crate) short: &'static [(&'static str, &'static str)],
}

fn lookup(table: &'static [(&'static str, &'static str)], key: &str) -> Option<&'static str> {
    table.binary_search_by(|(k, _)| (*k).cmp(key)).ok().map(|i| table[i].1)
}

impl Catalogue {
    /// The canonical name of a name or alias.
    pub(crate) fn canonical(&self, name: &str) -> Option<&'static str> {
        lookup(self.names, name)
    }

    /// The name an option is written with, or [`Error::UnknownOption`].
    pub(crate) fn written_name<'a>(&self, name: &'a str, style: OptionNames) -> Result<&'a str> {
        let canonical = self
            .canonical(name)
            .ok_or_else(|| Error::UnknownOption(format!("unknown {} option {name:?}", self.kind)))?;
        Ok(match style {
            OptionNames::Given => name,
            OptionNames::Canonical => canonical,
            OptionNames::Short => lookup(self.short, canonical).unwrap_or(canonical),
        })
    }

    /// Formats one option segment (`rs:fill:300:400`).
    pub(crate) fn format_entry(&self, (name, args): &Entry, sep: char, style: OptionNames) -> Result<String> {
        let written = self.written_name(name, style)?;
        if args.is_empty() {
            return Err(Error::InvalidArgument(format!("option {name}: at least one argument is required")));
        }
        let mut out = written.to_owned();
        for (i, a) in args.iter().enumerate() {
            out.push(sep);
            out.push_str(&format_arg(a, sep, &format!("option {name} argument {}", i + 1))?);
        }
        Ok(out)
    }
}

/// Whether a string argument contains a character that cannot appear in an option.
fn bad_arg_char(c: char, sep: char) -> bool {
    c == sep || matches!(c, '/' | '?' | '#' | '\u{feff}' | '\0'..='\x1f' | '\x7f') || c.is_whitespace()
}

/// Formats one argument. Strings are checked, never escaped: the server does
/// not unescape option arguments.
pub(crate) fn format_arg(a: &Arg, sep: char, where_: &str) -> Result<String> {
    match a {
        Arg::Null => Ok(String::new()),
        Arg::Bool(b) => Ok(if *b { "1" } else { "0" }.to_owned()),
        Arg::Int(i) => format_int(*i, where_),
        Arg::Float(f) => format_float(*f, where_),
        Arg::Str(s) => {
            if s.chars().any(|c| bad_arg_char(c, sep)) {
                return Err(Error::InvalidArgument(format!(
                    "{where_}: argument {s:?} contains a character that cannot appear in an option"
                )));
            }
            Ok(s.clone())
        }
    }
}
