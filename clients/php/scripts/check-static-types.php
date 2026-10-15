<?php

declare(strict_types=1);

// Checks that PHPStan understands the generated types: it runs PHPStan at
// level max on tests-static/usage.php and requires an error on every line
// marked "expect-error" and on no other line. Run: php scripts/check-static-types.php

$root = dirname(__DIR__);
$file = $root . '/tests-static/usage.php';
$cmd = [PHP_BINARY, $root . '/vendor/bin/phpstan', 'analyse', '--no-progress', '--error-format=json', '--memory-limit=2G', '-c', $root . '/tests-static/phpstan.neon'];
$proc = proc_open($cmd, [1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes, $root);
if (!is_resource($proc)) {
    fwrite(STDERR, "cannot run phpstan\n");
    exit(1);
}
$out = isset($pipes[1]) ? (string) stream_get_contents($pipes[1]) : '';
$err = isset($pipes[2]) ? (string) stream_get_contents($pipes[2]) : '';
proc_close($proc);

$report = json_decode($out, true);
if (!is_array($report) || !is_array($report['files'] ?? null)) {
    fwrite(STDERR, "phpstan did not print a JSON report:\n{$out}\n{$err}\n");
    exit(1);
}

$errors = [];
foreach ($report['files'] as $path => $f) {
    if (!is_array($f) || !is_array($f['messages'] ?? null)) {
        continue;
    }
    foreach ($f['messages'] as $m) {
        if (is_array($m) && is_int($m['line'] ?? null) && is_string($m['message'] ?? null)) {
            $errors[$m['line']][] = $m['message'];
        }
    }
}
foreach (is_array($report['errors'] ?? null) ? $report['errors'] : [] as $e) {
    fwrite(STDERR, 'phpstan: ' . (is_string($e) ? $e : json_encode($e)) . "\n");
    exit(1);
}

$lines = file($file);
if (false === $lines) {
    fwrite(STDERR, "cannot read {$file}\n");
    exit(1);
}
$bad = 0;
$want = 0;
foreach ($lines as $i => $line) {
    $n = $i + 1;
    $expected = str_contains($line, '// expect-error');
    $want += $expected ? 1 : 0;
    if ($expected && !isset($errors[$n])) {
        fwrite(STDERR, "line {$n}: PHPStan accepted it: " . trim($line) . "\n");
        ++$bad;
    }
    if (!$expected && isset($errors[$n])) {
        fwrite(STDERR, "line {$n}: unexpected error: " . implode('; ', $errors[$n]) . "\n");
        ++$bad;
    }
}
if (0 === $want) {
    fwrite(STDERR, "no expect-error lines in {$file}\n");
    exit(1);
}
if ($bad > 0) {
    exit(1);
}
echo "PHPStan rejects all {$want} invalid usages and accepts the valid ones\n";
