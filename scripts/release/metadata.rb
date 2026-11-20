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
metadata_methods = fields + %w[on_arm on_intel]
declarations = Hash.new { |hash, key| hash[key] = [] }
branches = {}
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

  if node[0] == :@ident && metadata_methods.include?(node[1])
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
    abort 'Cannot override Formula metadata methods' if metadata_methods.include?(node[1][1])

    scan_deferred.call(node)
  when :method_add_block
    call, block = node[1], node[2]
    unless call[0] == :method_add_arg && call[1][0] == :fcall && call[2] == []
      abort 'Unsupported Formula class-level block'
    end
    name = call[1][1][1]
    if %w[on_arm on_intel].include?(name)
      abort 'Duplicate architecture block' if branches.key?(name)
      unless block[0] == :do_block && block[1].nil? && block[2][0] == :bodystmt && block[2].drop(2).all?(&:nil?)
        abort 'Unsupported architecture block'
      end
      entries = Hash.new { |hash, key| hash[key] = [] }
      block[2][1].each do |entry|
        next if entry[0] == :void_stmt
        unless entry[0] == :command && %w[url sha256].include?(entry[1][1]) &&
               entry[2][0] == :args_add_block && entry[2][2] == false && literal.call(entry[2])
          abort 'Unsupported architecture metadata'
        end
        entries[entry[1][1]] << entry[1][2]
      end
      branches[name] = entries
    elsif name == 'test'
      # The test is deferred; reject declarations there without rejecting version.to_s.
      check_test = lambda do |part|
        next unless part.is_a?(Array)
        if %i[command fcall].include?(part[0]) && part[1].is_a?(Array) && metadata_methods.include?(part[1][1])
          abort 'Metadata must not be declared in the test block'
        end
        part.each { |child| check_test.call(child) if child.is_a?(Array) }
      end
      check_test.call(block)
    else
      abort 'Unsupported Formula class-level block'
    end
  else
    abort 'Unsupported conditional or indirect Formula declaration'
  end
end
declarations['branches'] = branches
puts JSON.generate(declarations)
