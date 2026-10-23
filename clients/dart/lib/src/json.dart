/// Decoding helpers of the generated response classes: each checks the
/// JSON type and throws a [FormatException] naming the field otherwise.
library;

/// A JSON object.
Map<String, Object?> jsonObject(Object? v, String where) => switch (v) {
      final Map<String, Object?> m => m,
      final Map<Object?, Object?> m => {
          for (final e in m.entries) '${e.key}': e.value,
        },
      _ => throw FormatException('$where: want an object, got $v'),
    };

/// A JSON array.
List<Object?> jsonList(Object? v, String where) => switch (v) {
      final List<Object?> l => l,
      _ => throw FormatException('$where: want an array, got $v'),
    };

/// A JSON string.
String jsonString(Object? v, String where) => switch (v) {
      final String s => s,
      _ => throw FormatException('$where: want a string, got $v'),
    };

/// A JSON integer (a whole number).
int jsonInt(Object? v, String where) => switch (v) {
      final int i => i,
      final double d when d.isFinite && d == d.truncateToDouble() => d.toInt(),
      _ => throw FormatException('$where: want an integer, got $v'),
    };

/// A JSON number.
double jsonDouble(Object? v, String where) => switch (v) {
      final num n => n.toDouble(),
      _ => throw FormatException('$where: want a number, got $v'),
    };

/// A JSON boolean.
bool jsonBool(Object? v, String where) => switch (v) {
      final bool b => b,
      _ => throw FormatException('$where: want a boolean, got $v'),
    };
