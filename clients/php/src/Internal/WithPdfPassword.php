<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

/**
 * withPdfPassword(): a password-protected PDF source.
 *
 * @internal
 */
trait WithPdfPassword
{
    /** Opens a password-protected PDF: the plain-text password is encrypted into pdp (needs both keys). */
    public function withPdfPassword(#[\SensitiveParameter] string $password): static
    {
        $this->state->pdfPassword = $password;

        return $this;
    }
}
