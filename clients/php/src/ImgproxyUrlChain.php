<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Internal\WithEncoding;
use Xtandard\Image\Internal\WithExtension;
use Xtandard\Image\Internal\WithPdfPassword;
use Xtandard\Image\Internal\WithPipeline;

/**
 * A fluent processing URL: one method per option name and alias, pipeline() for /-/.
 *
 *     $img->image('s3://bucket/cat.jpg')->resize('fill', 300, 400)->pipeline()->blur(2)->extension('webp')->build();
 */
final class ImgproxyUrlChain extends UrlChain
{
    use ProcessingMethods;
    use WithEncoding;
    use WithExtension;
    use WithPdfPassword;
    use WithPipeline;
}
