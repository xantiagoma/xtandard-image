<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

/**
 * 422: the source is too large, unsupported, corrupt or over a processing limit.
 */
final class UnprocessableException extends HttpException {}
