//! Option arguments and options values: [`Arg`], [`IntoArgs`], [`Options`], [`Pipelines`], [`Params`].

use std::borrow::Cow;

/// One option argument or query value, as written in a URL.
///
/// Booleans are written `1`/`0` in options and `true`/`false` in native
/// query keys; numbers as ECMAScript writes them (`300`, `1.5`, `1e-7`);
/// [`Arg::Null`] is an empty argument (`rs:fill:300::1`) and a skipped query key.
#[derive(Debug, Clone, PartialEq)]
pub enum Arg {
    /// An empty argument; a skipped query key.
    Null,
    /// A boolean.
    Bool(bool),
    /// An integer.
    Int(i128),
    /// A floating-point number; it must be finite.
    Float(f64),
    /// A string, written verbatim in options (never escaped) and percent-encoded in query values.
    Str(String),
}

macro_rules! arg_from_int {
    ($($t:ty),*) => {$(
        impl From<$t> for Arg {
            fn from(v: $t) -> Self {
                Self::Int(i128::from(v))
            }
        }
    )*};
}
arg_from_int!(i8, i16, i32, i64, i128, u8, u16, u32, u64);

impl From<usize> for Arg {
    fn from(v: usize) -> Self {
        // usize is at most 64 bits on every supported target.
        Self::Int(i128::try_from(v).unwrap_or(i128::MAX))
    }
}

impl From<isize> for Arg {
    fn from(v: isize) -> Self {
        Self::Int(i128::try_from(v).unwrap_or(i128::MAX))
    }
}

impl From<f64> for Arg {
    fn from(v: f64) -> Self {
        Self::Float(v)
    }
}

impl From<f32> for Arg {
    /// Converts through the shortest decimal of the `f32`, so `0.1_f32` is written `0.1`.
    fn from(v: f32) -> Self {
        Self::Float(v.to_string().parse().unwrap_or_else(|_| f64::from(v)))
    }
}

impl From<bool> for Arg {
    fn from(v: bool) -> Self {
        Self::Bool(v)
    }
}

impl From<&str> for Arg {
    fn from(v: &str) -> Self {
        Self::Str(v.to_owned())
    }
}

impl From<String> for Arg {
    fn from(v: String) -> Self {
        Self::Str(v)
    }
}

impl From<&String> for Arg {
    fn from(v: &String) -> Self {
        Self::Str(v.clone())
    }
}

impl From<Cow<'_, str>> for Arg {
    fn from(v: Cow<'_, str>) -> Self {
        Self::Str(v.into_owned())
    }
}

impl From<char> for Arg {
    fn from(v: char) -> Self {
        Self::Str(v.to_string())
    }
}

impl<T: Into<Arg>> From<Option<T>> for Arg {
    /// `None` is [`Arg::Null`].
    fn from(v: Option<T>) -> Self {
        v.map_or(Self::Null, Into::into)
    }
}

/// The arguments of one option: a bare value (`80`), a tuple (`("fill", 300, 400)`),
/// an array, a slice or a `Vec`.
///
/// ```
/// use xtandard_image::{Arg, IntoArgs, processing::ResizeType};
///
/// assert_eq!(80.into_args(), vec![Arg::Int(80)]);
/// assert_eq!(
///     (ResizeType::Fill, 300, None::<i32>, true).into_args(),
///     vec![Arg::Str("fill".into()), Arg::Int(300), Arg::Null, Arg::Bool(true)],
/// );
/// ```
pub trait IntoArgs {
    /// The argument list.
    fn into_args(self) -> Vec<Arg>;
}

macro_rules! into_args_scalar {
    ($($t:ty),*) => {$(
        impl IntoArgs for $t {
            fn into_args(self) -> Vec<Arg> {
                vec![self.into()]
            }
        }
    )*};
}
into_args_scalar!(
    i8,
    i16,
    i32,
    i64,
    i128,
    isize,
    u8,
    u16,
    u32,
    u64,
    usize,
    f32,
    f64,
    bool,
    char,
    &str,
    String,
    &String,
    Cow<'_, str>
);

impl IntoArgs for Arg {
    fn into_args(self) -> Vec<Arg> {
        vec![self]
    }
}

impl<T: Into<Arg>> IntoArgs for Option<T> {
    /// `None` is one empty argument.
    fn into_args(self) -> Vec<Arg> {
        vec![self.into()]
    }
}

impl<T: Into<Arg>> IntoArgs for Vec<T> {
    fn into_args(self) -> Vec<Arg> {
        self.into_iter().map(Into::into).collect()
    }
}

impl<T: Into<Arg>, const N: usize> IntoArgs for [T; N] {
    fn into_args(self) -> Vec<Arg> {
        self.into_iter().map(Into::into).collect()
    }
}

impl<T: Into<Arg> + Clone> IntoArgs for &[T] {
    fn into_args(self) -> Vec<Arg> {
        self.iter().cloned().map(Into::into).collect()
    }
}

impl IntoArgs for () {
    /// No arguments: building the URL fails with [`Error::InvalidArgument`](crate::Error::InvalidArgument).
    fn into_args(self) -> Vec<Arg> {
        Vec::new()
    }
}

macro_rules! into_args_tuple {
    ($($name:ident),+) => {
        impl<$($name: Into<Arg>),+> IntoArgs for ($($name,)+) {
            #[allow(non_snake_case)]
            fn into_args(self) -> Vec<Arg> {
                let ($($name,)+) = self;
                vec![$($name.into()),+]
            }
        }
    };
}
into_args_tuple!(A);
into_args_tuple!(A, B);
into_args_tuple!(A, B, C);
into_args_tuple!(A, B, C, D);
into_args_tuple!(A, B, C, D, E);
into_args_tuple!(A, B, C, D, E, F);
into_args_tuple!(A, B, C, D, E, F, G);
into_args_tuple!(A, B, C, D, E, F, G, H);
into_args_tuple!(A, B, C, D, E, F, G, H, I);
into_args_tuple!(A, B, C, D, E, F, G, H, I, J);
into_args_tuple!(A, B, C, D, E, F, G, H, I, J, K);
into_args_tuple!(A, B, C, D, E, F, G, H, I, J, K, L);

/// One option occurrence: a name (canonical or alias) and its arguments.
pub(crate) type Entry = (String, Vec<Arg>);

/// The options of one pipeline, keyed by canonical name or alias, in URL order.
///
/// Setting a name that is already present replaces its arguments in place, as a
/// mapping does; [`Options::set_opt`] with `None` skips the option.
///
/// ```
/// use xtandard_image::{ImgproxyUrlBuilder, Options, UrlParams, processing::ResizeType};
///
/// let opts = Options::new().set("rs", (ResizeType::Fill, 300, 400)).set("q", 80).set_opt("bl", None::<f64>);
/// let url = ImgproxyUrlBuilder::new().url("https://example.com/a.jpg", &opts, UrlParams::new())?;
/// assert_eq!(url, "/insecure/rs:fill:300:400/q:80/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw");
/// # Ok::<(), xtandard_image::Error>(())
/// ```
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Options {
    entries: Vec<Entry>,
}

impl Options {
    /// No options.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets an option (replacing the arguments of the same name in place).
    pub fn set(mut self, name: impl AsRef<str>, args: impl IntoArgs) -> Self {
        self.insert(name, args);
        self
    }

    /// Sets an option when `args` is `Some`; `None` skips it.
    pub fn set_opt<A: IntoArgs>(self, name: impl AsRef<str>, args: Option<A>) -> Self {
        match args {
            Some(a) => self.set(name, a),
            None => self,
        }
    }

    /// Sets an option in place (replacing the arguments of the same name).
    pub fn insert(&mut self, name: impl AsRef<str>, args: impl IntoArgs) {
        let name = name.as_ref();
        let args = args.into_args();
        if let Some(e) = self.entries.iter_mut().find(|(n, _)| n == name) {
            e.1 = args;
        } else {
            self.entries.push((name.to_owned(), args));
        }
    }

    /// Whether there are no options.
    pub fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }

    /// The number of options.
    pub fn len(&self) -> usize {
        self.entries.len()
    }

    /// The options in order: name as given and arguments.
    pub fn iter(&self) -> impl Iterator<Item = (&str, &[Arg])> {
        self.entries.iter().map(|(n, a)| (n.as_str(), a.as_slice()))
    }

    pub(crate) fn entries(&self) -> &[Entry] {
        &self.entries
    }
}

impl<K: AsRef<str>, A: IntoArgs> FromIterator<(K, A)> for Options {
    fn from_iter<I: IntoIterator<Item = (K, A)>>(iter: I) -> Self {
        let mut o = Self::new();
        for (k, a) in iter {
            o.insert(k, a);
        }
        o
    }
}

/// Option pipelines: one [`Options`] is one pipeline; several chain with `/-/`.
///
/// Anything that converts into it is accepted where a URL takes options:
/// `()` (none), an `Options` (or a reference), a `Vec`, array or slice of them.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Pipelines(pub Vec<Options>);

impl Pipelines {
    pub(crate) fn entries(&self) -> Vec<Vec<Entry>> {
        if self.0.is_empty() {
            return vec![Vec::new()];
        }
        self.0.iter().map(|o| o.entries().to_vec()).collect()
    }
}

impl From<()> for Pipelines {
    fn from((): ()) -> Self {
        Self(Vec::new())
    }
}

impl From<Options> for Pipelines {
    fn from(o: Options) -> Self {
        Self(vec![o])
    }
}

impl From<&Options> for Pipelines {
    fn from(o: &Options) -> Self {
        Self(vec![o.clone()])
    }
}

impl From<Option<Options>> for Pipelines {
    fn from(o: Option<Options>) -> Self {
        Self(o.into_iter().collect())
    }
}

impl From<Vec<Options>> for Pipelines {
    fn from(o: Vec<Options>) -> Self {
        Self(o)
    }
}

impl From<&[Options]> for Pipelines {
    fn from(o: &[Options]) -> Self {
        Self(o.to_vec())
    }
}

impl<const N: usize> From<[Options; N]> for Pipelines {
    fn from(o: [Options; N]) -> Self {
        Self(o.into())
    }
}

/// Query parameters in order (native `/q/` keys, imaginary parameters); keys may repeat.
/// A [`Arg::Null`] value skips its key.
///
/// ```
/// use xtandard_image::Params;
///
/// let p = Params::new().add("w", 300).add("f", "webp").add("h", None::<u32>);
/// assert_eq!(p.len(), 3);
/// ```
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Params(Vec<(String, Arg)>);

impl Params {
    /// No parameters.
    pub fn new() -> Self {
        Self::default()
    }

    /// Appends a parameter.
    pub fn add(mut self, key: impl Into<String>, value: impl Into<Arg>) -> Self {
        self.push(key, value);
        self
    }

    /// Appends a parameter in place.
    pub fn push(&mut self, key: impl Into<String>, value: impl Into<Arg>) {
        self.0.push((key.into(), value.into()));
    }

    /// Whether there are no parameters.
    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    /// The number of parameters (skipped ones included).
    pub fn len(&self) -> usize {
        self.0.len()
    }

    /// The parameters in order.
    pub fn iter(&self) -> impl Iterator<Item = (&str, &Arg)> {
        self.0.iter().map(|(k, v)| (k.as_str(), v))
    }
}

impl<K: Into<String>, V: Into<Arg>> FromIterator<(K, V)> for Params {
    fn from_iter<I: IntoIterator<Item = (K, V)>>(iter: I) -> Self {
        Self(iter.into_iter().map(|(k, v)| (k.into(), v.into())).collect())
    }
}

impl From<()> for Params {
    fn from((): ()) -> Self {
        Self::new()
    }
}

impl<K: Into<String>, V: Into<Arg>> From<Vec<(K, V)>> for Params {
    fn from(v: Vec<(K, V)>) -> Self {
        v.into_iter().collect()
    }
}

impl<K: Into<String>, V: Into<Arg>, const N: usize> From<[(K, V); N]> for Params {
    fn from(v: [(K, V); N]) -> Self {
        v.into_iter().collect()
    }
}
