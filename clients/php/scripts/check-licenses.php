<?php

declare(strict_types=1);

// Checks the licences of the package's runtime dependency closure (ADR 0006
// allowlist, ADR 0013): every installed package that is not a dev package
// (vendor/composer/installed.json) must be MIT, BSD, Apache-2.0 or ISC.
// There are no exceptions (the closure is the three PSR interface packages);
// one would be listed here with its reason, and in the README.
// Run after `composer install`: php scripts/check-licenses.php

const ALLOWED = '/^(MIT|MIT-0|BSD-2-Clause|BSD-3-Clause|Apache-2\.0|ISC)$/';

$installed = dirname(__DIR__) . '/vendor/composer/installed.json';
$text = file_get_contents($installed);
if (false === $text) {
    fwrite(STDERR, "cannot read {$installed}: run composer install\n");
    exit(1);
}
$data = json_decode($text, true, 512, JSON_THROW_ON_ERROR);
if (!is_array($data) || !is_array($data['packages'] ?? null)) {
    fwrite(STDERR, "{$installed} has no packages\n");
    exit(1);
}
$dev = is_array($data['dev-package-names'] ?? null) ? $data['dev-package-names'] : [];

$bad = 0;
$runtime = 0;
foreach ($data['packages'] as $p) {
    if (!is_array($p) || !is_string($p['name'] ?? null)) {
        continue;
    }
    $name = $p['name'];
    if (in_array($name, $dev, true)) {
        continue;
    }
    ++$runtime;
    $licences = is_array($p['license'] ?? null) ? array_values(array_filter($p['license'], 'is_string')) : [];
    $version = is_string($p['version'] ?? null) ? $p['version'] : '?';
    // Composer lists alternatives (OR); one allowed alternative is enough.
    $ok = [] !== array_filter($licences, static fn(string $l): bool => 1 === preg_match(ALLOWED, $l));
    $status = 'ok';
    if (!$ok) {
        $status = 'NOT ALLOWED';
        ++$bad;
    }
    printf("%-12s %s %s: %s\n", $status, $name, $version, implode(' OR ', $licences));
}
printf("%d runtime packages checked\n", $runtime);
exit($bad > 0 ? 1 : 0);
