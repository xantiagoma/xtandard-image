//! Macros the generated code expands: string-valued enums and option-name enums.

/// A string-valued enum: the variants, `ALL`, `as_str`, `Display`, `AsRef<str>`,
/// `FromStr` (unknown values are the given [`Error`](crate::Error) variant), and
/// the conversions into [`Arg`](crate::Arg) and [`IntoArgs`](crate::IntoArgs).
macro_rules! str_enum {
    (
        $(#[$meta:meta])*
        $vis:vis enum $name:ident ($err:ident) {
            $(
                $(#[$vmeta:meta])*
                $variant:ident = $value:literal $(| $alias:literal)*
            ),+ $(,)?
        }
    ) => {
        $(#[$meta])*
        #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord)]
        #[non_exhaustive]
        $vis enum $name {
            $(
                $(#[$vmeta])*
                $variant,
            )+
        }

        impl $name {
            /// Every value, in spec order.
            pub const ALL: &'static [Self] = &[$(Self::$variant),+];

            /// The value as written in URLs.
            pub const fn as_str(self) -> &'static str {
                match self {
                    $(Self::$variant => $value,)+
                }
            }
        }

        impl ::core::fmt::Display for $name {
            fn fmt(&self, f: &mut ::core::fmt::Formatter<'_>) -> ::core::fmt::Result {
                f.write_str(self.as_str())
            }
        }

        impl ::core::convert::AsRef<str> for $name {
            fn as_ref(&self) -> &str {
                self.as_str()
            }
        }

        impl ::core::str::FromStr for $name {
            type Err = $crate::Error;

            fn from_str(s: &str) -> ::core::result::Result<Self, Self::Err> {
                match s {
                    $($value $(| $alias)* => Ok(Self::$variant),)+
                    _ => Err($crate::Error::$err(::std::format!(
                        "{s:?} is not a {}",
                        stringify!($name)
                    ))),
                }
            }
        }

        impl ::core::convert::From<$name> for $crate::Arg {
            fn from(v: $name) -> Self {
                $crate::Arg::Str(v.as_str().into())
            }
        }

        impl $crate::IntoArgs for $name {
            fn into_args(self) -> ::std::vec::Vec<$crate::Arg> {
                ::std::vec![self.into()]
            }
        }
    };
}

pub(crate) use str_enum;

/// The option-name enum of a catalogue: canonical names, short names and aliases.
macro_rules! option_names {
    (
        $(#[$meta:meta])*
        $vis:vis enum $name:ident {
            $(
                $(#[$vmeta:meta])*
                $variant:ident = $value:literal, short $short:literal, aliases [$($alias:literal),*]
            ),+ $(,)?
        }
    ) => {
        $(#[$meta])*
        #[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, PartialOrd, Ord)]
        #[non_exhaustive]
        $vis enum $name {
            $(
                $(#[$vmeta])*
                $variant,
            )+
        }

        impl $name {
            /// Every option, in catalogue order.
            pub const ALL: &'static [Self] = &[$(Self::$variant),+];

            /// The canonical name.
            pub const fn name(self) -> &'static str {
                match self {
                    $(Self::$variant => $value,)+
                }
            }

            /// The name builders write by default (the shortest alias).
            pub const fn short(self) -> &'static str {
                match self {
                    $(Self::$variant => $short,)+
                }
            }

            /// The aliases, without the canonical name.
            pub const fn aliases(self) -> &'static [&'static str] {
                match self {
                    $(Self::$variant => &[$($alias),*],)+
                }
            }
        }

        impl ::core::fmt::Display for $name {
            fn fmt(&self, f: &mut ::core::fmt::Formatter<'_>) -> ::core::fmt::Result {
                f.write_str(self.name())
            }
        }

        impl ::core::convert::AsRef<str> for $name {
            fn as_ref(&self) -> &str {
                self.name()
            }
        }

        impl ::core::str::FromStr for $name {
            type Err = $crate::Error;

            /// Parses a canonical name or an alias.
            fn from_str(s: &str) -> ::core::result::Result<Self, Self::Err> {
                match s {
                    $($value $(| $alias)* => Ok(Self::$variant),)+
                    _ => Err($crate::Error::UnknownOption(::std::format!("unknown option {s:?}"))),
                }
            }
        }
    };
}
pub(crate) use option_names;
