#!/usr/bin/env ruby
# frozen_string_literal: true

# Static analysis of a Homebrew Formula: prints the line/column of every
# supported metadata declaration. Never evaluates the file.
require 'ripper'
require 'json'

tree = Ripper.sexp(STDIN.read)
abort 'Invalid Formula syntax' unless tree
statements = tree[1].reject { |node| node[0] == :void_stmt }
klass = statements[0]
parent = klass && klass[0] == :class ? klass[2] : nil
unless statements.length == 1 && klass[0] == :class &&
       klass[1][0] == :const_ref && klass[1][1][1] == 'Ianchor' &&
       parent&.first == :var_ref && parent.dig(1, 0) == :@const && parent.dig(1, 1) == 'Formula'
  abort 'Expected a single static Ianchor < Formula class'
end
body = klass[3]
abort 'Unsupported Formula class body' unless body[0] == :bodystmt && body.drop(2).all?(&:nil?)
fields = %w[version url sha256 depends_on]
declarations = Hash.new { |hash, key| hash[key] = [] }
literal_nodes = %i[args_add_block string_literal string_content symbol_literal symbol array hash assoclist_from_args bare_assoc_hash assoc_new
                   string_concat]
literal = lambda do |node|
  next true unless node.is_a?(Array)
  next node.all? { |child| literal.call(child) } unless node[0].is_a?(Symbol)

  next true if node[0].to_s.start_with?('@')

  literal_nodes.include?(node[0]) && node.drop(1).all? { |child| literal.call(child) }
end
scan_deferred = lambda do |node|
  next unless node.is_a?(Array)

  if node[0] == :@ident && fields.include?(node[1])
    abort 'Metadata must be an unconditional direct Formula class declaration'
  end
  node.each { |child| scan_deferred.call(child) if child.is_a?(Array) }
end
body[1].each do |node|
  case node[0]
  when :void_stmt
    next
  when :command
    token, args = node[1], node[2]
    unless (fields + %w[desc homepage license]).include?(token[1]) &&
           args[0] == :args_add_block && args[2] == false && literal.call(args)
      abort 'Unsupported dynamic Formula declaration; use static literal DSL fields'
    end
    declarations[token[1]] << token[2] if fields.include?(token[1])
  when :method_add_arg
    call = node[1]
    unless call[0] == :fcall && call[1][1] == 'deny_network_access!' && node[2] == []
      abort 'Unsupported Formula class-level call'
    end
  when :def
    abort 'Cannot override Formula metadata methods' if fields.include?(node[1][1])

    scan_deferred.call(node)
  when :method_add_block
    call = node[1]
    unless call[0] == :method_add_arg && call[1][0] == :fcall &&
           call[1][1][1] == 'test' && call[2] == []
      abort 'Unsupported Formula class-level block'
    end
    # Homebrew registers this block; it does not run while reading metadata.
  else
    abort 'Unsupported conditional or indirect Formula declaration'
  end
end
puts JSON.generate(declarations)
