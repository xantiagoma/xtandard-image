<?php

declare(strict_types=1);

namespace Xtandard\Image\Exception;

/**
 * 404: the source does not exist (not_found) or the URL expired (expired).
 */
final class NotFoundException extends HttpException {}
