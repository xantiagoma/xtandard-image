<?php

declare(strict_types=1);

// Static type checks of the generated option shapes, run by
// scripts/check-static-types.php (`task types`): PHPStan must report an
// error on every line marked "expect-error" and on no other line. It is
// never executed. PHPStan's array shapes are unsealed, so an unknown option
// name in an options array is not a static error there (Psalm reports it);
// the builders throw unknown_option at run time, and the fluent methods are
// checked statically.

namespace Xtandard\Image\StaticChecks;

use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\Pipelines;
use Xtandard\Image\QueryUrlBuilder;
use Xtandard\Image\StreamProtocol;

/**
 * @phpstan-import-type ProcessingOptions from \Xtandard\Image\OptionTypes
 * @phpstan-import-type InfoResponse from \Xtandard\Image\ResponseTypes
 */
final class Usage
{
    /** @param InfoResponse $info */
    public function valid(ImgproxyUrlBuilder $img, QueryUrlBuilder $q, array $info): int
    {
        /** @var ProcessingOptions $opts */
        $opts = ['rs' => ['fill', 300, 400, true], 'q' => 80, 'g' => 'sm', 'bl' => null, 'min-width' => 10];
        $img->url('s3://b/cat.jpg', $opts, 'webp');
        $img->url('s3://b/cat.jpg', new Pipelines(['rs' => ['fit', 500, 500]], ['bl' => 2.5]));
        $q->videoUrl('s3://m/a.mov', new Pipelines(['vs' => 1], ['vs' => 2]), 'webm');
        $img->url('s3://b/cat.jpg', ['rs' => ['fill', null, null, true]], sourceEncoding: 'plain');
        $img->infoUrl('s3://b/cat.jpg', ['size' => true, 'format' => true]);
        $img->videoUrl('s3://m/a.mov', ['vs' => 12, 'vd' => 8], 'webm');
        $img->streamUrl('s3://m/a.mov', StreamProtocol::Hls, [360, 720], 4, 'ts');
        $img->streamUrl('s3://m/a.mov', 'dash');
        $q->url('s3://b/cat.jpg', ['w' => 300, 'f' => 'webp'], ['bl' => 2]);
        $q->imaginaryUrl('resize', 's3://b/cat.jpg', ['width' => 300]);
        $img->image('s3://b/cat.jpg')->resize('fill', 300, 400)->quality(80)->extension('webp')->build();

        return ($info['width'] ?? 0) + \count($info['exif'] ?? []);
    }

    /** @param InfoResponse $info */
    public function invalid(ImgproxyUrlBuilder $img, QueryUrlBuilder $q, array $info): string
    {
        $img->url('s3://b/cat.jpg', ['rs' => ['stretch', 300]]); // expect-error: not a resize type
        $img->url('s3://b/cat.jpg', ['q' => 'high']); // expect-error: quality is a number
        $img->url('s3://b/cat.jpg', ['bl' => ['two']]); // expect-error: blur is a number
        $img->url('s3://b/cat.jpg', sourceEncoding: 'hex'); // expect-error: not an encoding
        $img->url('s3://b/cat.jpg', new Pipelines(['rs' => ['fit', 1, 1]], ['bl' => 'x'])); // expect-error: blur is a number
        $img->infoUrl('s3://b/cat.jpg', ['size' => 'yes']); // expect-error: size is a boolean
        $img->videoUrl('s3://m/a.mov', ['vd' => 'long']); // expect-error: duration is a number
        $img->streamUrl('s3://m/a.mov', 'smooth'); // expect-error: not a protocol
        $q->url('s3://b/cat.jpg', ['w' => [300]]); // expect-error: native values are scalars
        $q->imaginaryUrl('resise', 's3://b/cat.jpg'); // expect-error: not an imaginary endpoint
        $img->image('s3://b/cat.jpg')->resize(['fill']); // expect-error: arguments are scalars
        $img->image('s3://b/cat.jpg')->blurr(2); // expect-error: no such method

        return $info['format_name']; // expect-error: not an InfoResponse key
    }
}
