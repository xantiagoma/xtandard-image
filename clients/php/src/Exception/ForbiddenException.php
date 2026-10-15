<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

/**
 * 403: a bad or missing signature (unauthorized) or a refused source or option (forbidden).
 */
final class ForbiddenException extends HttpException {}
