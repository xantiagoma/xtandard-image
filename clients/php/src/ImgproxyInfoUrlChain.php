<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Internal\WithEncoding;
use Xtandard\Image\Internal\WithPdfPassword;

/**
 * A fluent /info URL: one method per /info option name and alias.
 *
 *     $img->info('s3://bucket/cat.jpg')->size(true)->format(true)->build();
 */
final class ImgproxyInfoUrlChain extends UrlChain
{
    use InfoMethods;
    use WithEncoding;
    use WithPdfPassword;
}
