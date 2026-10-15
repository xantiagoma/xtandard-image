<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Internal\WithEncoding;
use Xtandard\Image\Internal\WithExtension;

/**
 * A fluent /video URL: video transformations, audio and waveforms.
 *
 *     $img->video('s3://media/talk.mov')->start(12)->duration(8)->mute(true)->extension('mp4')->build();
 */
final class ImgproxyVideoUrlChain extends UrlChain
{
    use VideoMethods;
    use WithEncoding;
    use WithExtension;
}
