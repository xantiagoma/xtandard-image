import 'dart:convert';
import 'dart:io';

/// Reads a repository file relative to the package root (clients/dart).
Map<String, Object?>? loadSync(String rel) =>
    jsonDecode(File('../../$rel').readAsStringSync()) as Map<String, Object?>;
