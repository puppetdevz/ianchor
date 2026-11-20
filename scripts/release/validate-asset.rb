#!/usr/bin/env ruby
# frozen_string_literal: true

# Validate a gzip-compressed ustar archive without extracting untrusted paths.
require 'zlib'

path, arch = ARGV
cpu = { 'arm64' => 0x0100000c, 'amd64' => 0x01000007 }[arch]
abort 'usage: validate-asset.rb ARCHIVE arm64|amd64' unless path && cpu
abort 'Asset must be a regular, non-symlink file' if File.symlink?(path) || !File.file?(path)

def number(field)
  value = field.delete("\0 ")
  abort 'Invalid tar number' unless value.empty? || value.match?(/\A[0-7]+\z/)
  value.empty? ? 0 : value.to_i(8)
end

def exact(gzip, size)
  data = gzip.read(size)
  abort 'Truncated asset archive' unless data && data.bytesize == size
  data
end

seen = {}
binary = false
File.open(path, 'rb') do |file|
  gzip = Zlib::GzipReader.new(file)
  loop do
    header = exact(gzip, 512)
    if header == "\0" * 512
      abort 'Invalid tar terminator' unless exact(gzip, 512) == "\0" * 512
      while (tail = gzip.read(65_536)) && !tail.empty?
        abort 'Trailing tar data' unless tail.delete("\0").empty?
      end
      abort 'Trailing gzip data' unless gzip.unused.to_s.empty? && file.eof?
      break
    end
    checksum = number(header.byteslice(148, 8))
    check_header = header.dup
    check_header[148, 8] = ' ' * 8
    abort 'Invalid tar checksum' unless check_header.bytes.sum == checksum
    name = header.byteslice(0, 100).split("\0", 2)[0].to_s
    prefix = header.byteslice(345, 155).split("\0", 2)[0].to_s
    name = "#{prefix}/#{name}" unless prefix.empty?
    name = name.delete_prefix('./')
    type = header.getbyte(156)
    size = number(header.byteslice(124, 12))
    mode = number(header.byteslice(100, 8))
    abort 'Unsafe or duplicate archive path' unless name.match?(%r{\A[A-Za-z0-9_.-]+/?\z}) && !%w[. ..].include?(name.delete_suffix('/')) && !seen[name.delete_suffix('/')]
    seen[name.delete_suffix('/')] = true
    abort 'Unsupported archive entry' unless [0, 48, 53].include?(type)
    abort 'Invalid directory entry' if type == 53 && size != 0
    abort 'Asset entry too large' if size > 1_073_741_824
    if name == 'ianchor' && type != 53
      abort 'Invalid executable mode or size' if (mode & 0o111).zero? || size < 32
      mach = exact(gzip, 32)
      magic, actual_cpu, _subtype, filetype, ncmds, command_bytes = mach.unpack('a4V5')
      abort 'Wrong executable architecture or format' unless magic == "\xcf\xfa\xed\xfe".b && actual_cpu == cpu && filetype == 2
      abort 'Invalid Mach-O load commands' if ncmds.zero? || command_bytes > size - 32 || command_bytes > 16_777_216 || ncmds > command_bytes / 8
      commands = exact(gzip, command_bytes)
      offset = 0
      executable_segment = false
      ncmds.times do
        abort 'Truncated Mach-O load command' if offset + 8 > commands.bytesize
        command, length = commands.byteslice(offset, 8).unpack('V2')
        abort 'Invalid Mach-O load command size' if length < 8 || length % 8 != 0 || offset + length > commands.bytesize
        if command == 0x19 # LC_SEGMENT_64
          abort 'Invalid Mach-O segment' if length < 72
          segment = commands.byteslice(offset, length)
          file_offset, file_size = segment.byteslice(40, 16).unpack('Q<2')
          permissions = segment.byteslice(60, 4).unpack1('V')
          abort 'Mach-O segment exceeds executable' if file_offset > size || file_size > size - file_offset
          executable_segment ||= file_size > 0 && (permissions & 4) != 0
        end
        offset += length
      end
      abort 'Invalid Mach-O command table' unless offset == command_bytes && executable_segment
      binary = true
      remaining = size - 32 - command_bytes
    else
      remaining = size
    end
    while remaining > 0
      chunk = [remaining, 65_536].min
      exact(gzip, chunk)
      remaining -= chunk
    end
    padding = (512 - size % 512) % 512
    exact(gzip, padding) if padding > 0
  end
  gzip.close
end
abort 'Archive must contain a root ianchor executable' unless binary
