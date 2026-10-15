# frozen_string_literal: true

module XtandardImage
  # What a fluent chain collected.
  # @api private
  ChainState = Struct.new(:source, :pipelines, :params, :encoding, :ext, :pdf_password)

  # Shared state and control methods of the fluent builders. A chain is
  # mutable: every method appends to it and returns +self+; +build+ (or
  # +to_s+) writes the URL.
  class Chain
    # @api private
    attr_reader :chain_state

    # @api private
    def initialize(source, runner)
      @chain_state = ChainState.new(source, [[]], [], nil, nil, nil)
      @runner = runner
    end

    # Appends one option occurrence (as the per-option methods do).
    def option(name, *args)
      current_pipeline << [name.to_s, args]
      self
    end

    # Appends every option of an options Hash, in key order.
    def options(options)
      current_pipeline.concat(Options.entries_of(options))
      self
    end

    # Builds the URL.
    def build = @runner.call(chain_state)

    alias to_s build

    def inspect = "#<#{self.class.name} #{chain_state.source.inspect}>"

    private

    def current_pipeline = chain_state.pipelines.fetch(-1)
  end

  # +encoding+: the source encoding of one URL.
  module WithEncoding
    # Overrides the source encoding (:base64, :plain or :encrypted).
    def encoding(encoding)
      chain_state.encoding = encoding
      self
    end
  end

  # +extension+: the output format or container.
  module WithExtension
    # Sets the output extension (@webp / .webp, a container for /video).
    def extension(ext)
      chain_state.ext = ext
      self
    end
  end

  # +pipeline+: chained pipelines.
  module WithPipeline
    # Starts the next chained pipeline (/-/).
    def pipeline
      chain_state.pipelines << []
      self
    end
  end

  # +with_pdf_password+: a password-protected PDF source.
  module WithPassword
    # Opens a password-protected PDF: the plain-text password is encrypted
    # into pdp (needs both keys).
    def with_pdf_password(password)
      chain_state.pdf_password = password
      self
    end
  end
end
