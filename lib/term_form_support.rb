# frozen_string_literal: true

module TermForm
  module Support
    module_function

    def key(value)
      result = value.to_sym
      raise ArgumentError, "key cannot be empty" if result.to_s.empty?

      result
    rescue NoMethodError
      raise ArgumentError, "key must be convertible to a Symbol"
    end

    def copy(value)
      case value
      when String
        value.dup
      when Array
        value.map { |entry| copy(entry) }
      when Hash
        value.each_with_object({}) { |(key, entry), result| result[copy(key)] = copy(entry) }
      when Struct
        copied = value.each_pair.to_h { |name, entry| [name, copy(entry)] }
        if value.class.respond_to?(:keyword_init?) && value.class.keyword_init?
          value.class.new(**copied)
        else
          value.class.new(*value.members.map { |name| copied.fetch(name) })
        end
      else
        value.dup
      end
    rescue TypeError
      value
    end

    def frozen_copy(value)
      copied = copy(value)
      deep_freeze(copied)
    end

    def deep_freeze(value)
      case value
      when Array
        value.each { |entry| deep_freeze(entry) }
      when Hash
        value.each { |key, entry| deep_freeze(key); deep_freeze(entry) }
      when Struct
        value.each_pair { |_name, entry| deep_freeze(entry) }
      end
      value.freeze
    end

    def property(value, context)
      return value unless value.respond_to?(:call)

      value.arity.zero? ? value.call : value.call(context)
    end

    def callable(callable, value, context)
      case callable.arity
      when 0 then callable.call
      when 1 then callable.call(value)
      else callable.call(value, context)
      end
    end
  end
end
