<?php

declare(strict_types=1);

namespace Xtandard\Image;

/**
 * How option names are written in URLs.
 */
enum OptionNameStyle: string
{
    /** Each option's short name (rs, q, bl): the default. */
    case Short = 'short';
    /** The canonical name (resize, quality, blur). */
    case Canonical = 'canonical';
    /** The name or alias as the caller wrote it. */
    case Given = 'given';
}
