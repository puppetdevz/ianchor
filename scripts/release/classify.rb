#!/usr/bin/env ruby
# frozen_string_literal: true

# Classifies a failed GitHub API query without echoing its (possibly secret)
# stderr. Usage: classify.rb ERROR_FILE EXIT_CODE HTTP_STATUS
error_file, exit_code, raw_status = ARGV
status = raw_status.to_s.match?(/\A\d{3}\z/) ? raw_status : 'unknown'
error = File.exist?(error_file) ? File.read(error_file) : ''
category = 'API/network'
guidance = 'Check GitHub API availability, credentials and proxy connectivity'
if %w[401 403].include?(status)
  category = 'authentication/permission'
  guidance = 'Check SOURCE_TOKEN contents permissions and repository access'
elsif status == '429'
  category = 'rate limit'
  guidance = 'Wait for the GitHub API rate limit to reset'
elsif status.match?(/\A5\d\d\z/)
  category = 'GitHub service'
  guidance = 'Check GitHub API service status'
elsif error.match?(/no such host|ENOTFOUND|name resolution|resolve host/i)
  category = 'DNS'
  guidance = 'Check the DNS resolver and api.github.com connectivity'
elsif error.match?(/TLS|SSL|x509|certificate/i)
  category = 'TLS'
  guidance = 'Check TLS certificates and HTTPS proxy configuration'
elsif error.match?(/timed? ?out|timeout|ECONN|connection|proxy/i)
  category = 'network'
  guidance = 'Check network and proxy connectivity'
end
warn "::error::Release query failed (#{category}; exit=#{exit_code}, HTTP=#{status}). " \
     "#{guidance}; rerun the same tag after resolving. Refusing to infer absence; existing Releases are preserved."
exit 1
