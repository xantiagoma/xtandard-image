// Loads the repository's shared fixtures (clients/spec/conformance.json,
// testdata/urls/imgproxy-builder.json, testdata/security/vectors.json) by
// relative path from the package root.
//
// On the VM the file is read synchronously, so every case is its own test.
// Browsers cannot read files: there a hybrid isolate (spawnHybridCode, run
// on the VM by package:test) reads it, inside setUpAll, and one test runs
// every case and reports all failures.

import 'dart:convert';

import 'package:test/test.dart';

import 'fixtures_stub.dart' if (dart.library.io) 'fixtures_io.dart' as impl;

/// A JSON object.
typedef Json = Map<String, Object?>;

/// Thrown by a case runner for a case the Dart API cannot express.
final class NotExpressible implements Exception {
  /// Creates the marker.
  const NotExpressible(this.reason);

  /// Why the case is skipped.
  final String reason;
}

const String _hybrid = r'''
import 'dart:io';

import 'package:stream_channel/stream_channel.dart';

Future<void> hybridMain(StreamChannel<Object?> channel, Object? message) async {
  channel.sink.add(await File('../../${message as String}').readAsString());
  await channel.sink.close();
}
''';

Future<Json> _loadHybrid(String rel) async {
  final channel = spawnHybridCode(_hybrid, message: rel);
  final text = await channel.stream.first as String;
  return jsonDecode(text) as Json;
}

/// Declares the tests of a fixture: [cases] picks the cases out of the
/// decoded file, [name] names one, and [run] checks one (throwing
/// [NotExpressible] to skip it). [skipped] lists the names of the cases
/// expected to be skipped, which the browser run checks.
void fixtureTests(
  String rel, {
  required List<Json> Function(Json fixture) cases,
  required String Function(Json c) name,
  required void Function(Json fixture, Json c) run,
  Set<String> skipped = const {},
}) {
  final sync = impl.loadSync(rel);
  if (sync != null) {
    final all = cases(sync);
    test('$rel has cases', () => expect(all, isNotEmpty));
    for (final c in all) {
      test(name(c), () {
        try {
          run(sync, c);
        } on NotExpressible catch (e) {
          expect(skipped, contains(name(c)), reason: 'unexpected skip');
          markTestSkipped(e.reason);
        }
      });
    }
    return;
  }
  late Json fixture;
  setUpAll(() async => fixture = await _loadHybrid(rel));
  test('every case of $rel', () {
    final failures = <String>[];
    final skips = <String>{};
    var passed = 0;
    for (final c in cases(fixture)) {
      try {
        run(fixture, c);
        passed++;
      } on NotExpressible {
        skips.add(name(c));
      } on Object catch (e) {
        failures.add('${name(c)}: $e');
      }
    }
    expect(failures, isEmpty);
    expect(skips, skipped);
    expect(passed, greaterThan(0));
    printOnFailure('$passed passed, ${skips.length} skipped');
  });
}
