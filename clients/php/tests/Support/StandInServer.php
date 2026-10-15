<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests\Support;

use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\SigningKey;

/**
 * Runs tests/Support/server.php under PHP's built-in web server on a free port.
 */
final class StandInServer
{
    public const KEY = '736563726574';

    public const SALT = '68656C6C6F';

    /** @var resource */
    private $process;

    private function __construct(public readonly string $baseUrl, mixed $process)
    {
        if (!\is_resource($process)) {
            throw new \RuntimeException('cannot start the stand-in server');
        }
        $this->process = $process;
    }

    public static function start(): self
    {
        $port = self::freePort();
        $cmd = [\PHP_BINARY, '-S', "127.0.0.1:{$port}", __DIR__ . '/server.php'];
        $null = \PHP_OS_FAMILY === 'Windows' ? 'NUL' : '/dev/null';
        $proc = proc_open($cmd, [0 => ['file', $null, 'r'], 1 => ['file', $null, 'w'], 2 => ['file', $null, 'w']], $pipes);
        $server = new self("http://127.0.0.1:{$port}", $proc);
        // Wait (up to 10 s) until the server accepts connections.
        for ($i = 0; $i < 200; ++$i) {
            $sock = @fsockopen('127.0.0.1', $port, $errno, $errstr, 0.05);
            if (false !== $sock) {
                fclose($sock);

                return $server;
            }
            usleep(50_000);
        }
        $server->stop();

        throw new \RuntimeException('the stand-in server did not start');
    }

    public function stop(): void
    {
        proc_terminate($this->process);
        proc_close($this->process);
    }

    public static function freePort(): int
    {
        $sock = stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
        if (false === $sock) {
            throw new \RuntimeException("no free port: {$errstr}");
        }
        $name = (string) stream_socket_get_name($sock, false);
        fclose($sock);

        return (int) substr($name, (int) strrpos($name, ':') + 1);
    }

    public static function signing(): SigningKey
    {
        return new SigningKey(self::KEY, self::SALT);
    }

    /** The only /video path (and query) the stand-in accepts: a signed URL for s3://m/clip.mov. */
    public static function videoPath(): string
    {
        return (new ImgproxyUrlBuilder(signing: self::signing()))->videoUrl('s3://m/clip.mov', ['vd' => 2], 'webm');
    }

    /** The /video body: 1000 bytes, 0 to 255 repeating. */
    public static function body(): string
    {
        $out = '';
        for ($i = 0; $i < 1000; ++$i) {
            $out .= \chr($i % 256);
        }

        return $out;
    }
}
