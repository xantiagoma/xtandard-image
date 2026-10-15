<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Internal\WithExtension;

/**
 * A fluent /q/video URL: one method per video option name and alias.
 *
 *     $q->video('s3://media/talk.mov')->start(1)->duration(5)->extension('webm')->build();
 */
final class QueryVideoUrlChain extends UrlChain
{
    use VideoMethods;
    use WithExtension;
}
