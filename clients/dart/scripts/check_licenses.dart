// Checks the licences of the package's runtime dependency closure against
// the ADR 0006 allowlist (ADR 0013): MIT, BSD-2/3-Clause, Apache-2.0, ISC.
//
// Run after `dart pub get`: `dart run scripts/check_licenses.dart`.
//
// pub has no licence metadata, so the closure comes from the resolved
// package config and each package's pubspec `dependencies` (never
// dev_dependencies), and each licence from the package's LICENSE file,
// classified by its text. There are no exceptions; anything unrecognised
// fails.
// ignore_for_file: avoid_print

import 'dart:convert';
import 'dart:io';

import 'package:yaml/yaml.dart';

const String root = 'xtandard_image';

/// Licences by a phrase only their text contains.
const Map<String, List<String>> allowed = {
  'Apache-2.0': ['Apache License', 'Version 2.0'],
  'MIT': ['Permission is hereby granted, free of charge'],
  'BSD-3-Clause': [
    'Redistribution and use in source and binary forms',
    'Neither the name',
  ],
  'BSD-2-Clause': ['Redistribution and use in source and binary forms'],
  'ISC': ['Permission to use, copy, modify, and/or distribute this software'],
};

/// Phrases of licences outside the allowlist.
const List<String> refused = [
  'GNU GENERAL PUBLIC LICENSE',
  'GNU LESSER GENERAL PUBLIC LICENSE',
  'GNU AFFERO GENERAL PUBLIC LICENSE',
  'Mozilla Public License',
  'Eclipse Public License',
  'Server Side Public License',
  'Business Source License',
];

Map<String, Directory> packageRoots() {
  final configFile = File('.dart_tool/package_config.json');
  final config =
      jsonDecode(configFile.readAsStringSync()) as Map<String, Object?>;
  final base = configFile.parent.uri;
  return {
    for (final p in (config['packages'] as List).cast<Map<String, Object?>>())
      p['name'] as String:
          Directory.fromUri(base.resolve(p['rootUri'] as String)),
  };
}

List<String> runtimeDependencies(Directory dir) {
  final pubspec = loadYaml(
    File('${dir.path}/pubspec.yaml').readAsStringSync(),
  ) as YamlMap;
  final deps = pubspec['dependencies'] as YamlMap?;
  if (deps == null) return const [];
  return [
    for (final MapEntry(:key, :value) in deps.entries)
      // SDK packages (flutter, flutter_web_plugins) are not pub packages.
      if (!(value is YamlMap && value.containsKey('sdk'))) key as String,
  ];
}

String? licenceText(Directory dir) {
  for (final name in ['LICENSE', 'LICENSE.md', 'LICENSE.txt', 'COPYING']) {
    final f = File('${dir.path}/$name');
    if (f.existsSync()) return f.readAsStringSync();
  }
  return null;
}

String classify(String text) {
  for (final bad in refused) {
    if (text.contains(bad)) return 'NOT ALLOWED ($bad)';
  }
  for (final MapEntry(:key, :value) in allowed.entries) {
    if (value.every(text.contains)) return key;
  }
  return 'UNKNOWN';
}

void main() {
  final roots = packageRoots();
  final seen = <String>{};
  final todo = [root];
  while (todo.isNotEmpty) {
    final name = todo.removeLast();
    if (!seen.add(name)) continue;
    final dir = roots[name];
    if (dir == null) {
      stderr.writeln(
          '$name is not in .dart_tool/package_config.json: run `dart pub get`');
      exit(1);
    }
    todo.addAll(runtimeDependencies(dir));
  }
  var bad = 0;
  for (final name in (seen..remove(root)).toList()..sort()) {
    final text = licenceText(roots[name]!);
    final licence = text == null ? 'NO LICENSE FILE' : classify(text);
    final ok = allowed.containsKey(licence);
    if (!ok) bad++;
    print('${ok ? 'ok' : 'FAIL'}  $name: $licence');
  }
  print('${seen.length} runtime dependencies, $bad not allowed');
  if (bad > 0) exit(1);
}
