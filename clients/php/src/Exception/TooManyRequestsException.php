<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

/**
 * 429: the server is overloaded; retry later.
 */
final class TooManyRequestsException extends HttpException {}
