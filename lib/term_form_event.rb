# frozen_string_literal: true

require_relative "term_form_support"

module TermForm
  class Event
    KEY_BYTES = {
      tab: "\t", shift_tab: "\e[Z", return: "\r", escape: "\e",
      up: "\e[A", down: "\e[B", left: "\e[D", right: "\e[C",
      home: "\e[H", end: "\e[F", delete: "\e[3~", backspace: "\x7f",
    }.freeze

    attr_reader :type, :payload, :raw

    def self.normalize(value)
      case value
      when self then value
      when Symbol, String then new(value)
      when Hash
        attributes = value.dup
        type = attributes.delete(:type) || attributes.delete("type")
        raise ArgumentError, "event hash requires :type" unless type

        new(type, attributes)
      else
        raise ArgumentError, "cannot normalize #{value.class} as an event"
      end
    end

    def self.key(key)
      raw = key.is_a?(Symbol) ? KEY_BYTES.fetch(key) { raise ArgumentError, "unknown key: #{key}" } : key
      new(:key, { key: key }, raw: raw)
    end
    def self.paste(text) = new(:paste, text: text)

    def initialize(type, payload = nil, raw: nil, **attributes)
      @type = Support.key(type)
      data = payload.nil? ? attributes : payload
      raise ArgumentError, "event payload must be a Hash" unless data.is_a?(Hash)

      @payload = Support.frozen_copy(data)
      @raw = Support.frozen_copy(raw)
      freeze
    end

    def [](key)
      return @payload[key] if @payload.key?(key)

      @payload[key.to_s]
    end
    def key = self[:key]
    def value = self[:value]
    def text = self[:text]

    def ==(other)
      other.is_a?(Event) && type == other.type && payload == other.payload && raw == other.raw
    end
  end

  class Transition
    TYPES = %i[
      unhandled handled changed focus_changed invalid commit_requested
      commit_pending commit_accepted commit_rejected cancel_requested refreshed
    ].freeze

    attr_reader :type, :event, :data

    def initialize(type, event: nil, **data)
      @type = Support.key(type)
      raise ArgumentError, "unknown transition type: #{@type}" unless TYPES.include?(@type)

      @event = event && Event.normalize(event)
      @data = data.each_with_object({}) do |(key, value), copied|
        copied[Support.frozen_copy(key)] = if defined?(CommitRequest) && value.is_a?(CommitRequest)
                                             value
                                           else
                                             Support.frozen_copy(value)
                                           end
      end.freeze
      freeze
    end

    TYPES.each { |candidate| define_method("#{candidate}?") { @type == candidate } }

    def [](key) = @data[key]
    def focus_key = @data[:focus_key]
    def render_model = @data[:render_model]
    def request = @data[:request]
    def errors = @data[:errors]
  end

  class KeyMap
    DEFAULT_BINDINGS = {
      "\t" => :next,
      "\e[B" => :next,
      "\e[Z" => :previous,
      "\e[A" => :previous,
      "\r" => :commit,
      "\n" => :commit,
      "\e" => :cancel,
    }.freeze

    def initialize(bindings = {}, defaults: true)
      raise ArgumentError, "bindings must be a Hash" unless bindings.is_a?(Hash)

      source = defaults ? DEFAULT_BINDINGS.merge(bindings) : bindings
      @bindings = source.each_with_object({}) do |(raw, event), result|
        result[Support.frozen_copy(raw)] = event.is_a?(Event) ? event : Event.normalize(event)
      end.freeze
      freeze
    end

    def event_for(raw)
      bound = @bindings[raw]
      return Event.new(:input, { text: raw }, raw: raw) unless bound

      Event.new(bound.type, bound.payload, raw: raw)
    end
    alias call event_for
  end
end
