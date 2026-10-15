<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

/**
 * 502 and 504: an upstream error or timeout.
 */
final class GatewayException extends ServerException {}
