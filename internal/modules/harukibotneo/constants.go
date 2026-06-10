package harukibotneo

import "time"

const (
	verifyCodeTTL     = 10 * time.Minute
	verifyCodeLen     = 6
	maxVerifyAttempts = 5

	sendMailRateLimitWindow = 60 * time.Minute
	sendMailIPLimit         = 20
	sendMailTargetLimit     = 5

	registerRateLimitWindow = 10 * time.Minute
	registerTargetLimit     = 5

	botIDMin     = 10000000
	botIDMax     = 99999999
	botIDRetries = 10

	credentialBytes = 32

	rateLimitLimitedByNone   = int64(0)
	rateLimitLimitedByIP     = int64(1)
	rateLimitLimitedByTarget = int64(2)

	sendMailRateLimitScript = `
local ipCount = redis.call('INCR', KEYS[1])
if ipCount == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[3])
end
local targetCount = redis.call('INCR', KEYS[2])
if targetCount == 1 then
  redis.call('PEXPIRE', KEYS[2], ARGV[3])
end
if ipCount > tonumber(ARGV[1]) then
  return {1, ipCount, targetCount}
end
if targetCount > tonumber(ARGV[2]) then
  return {2, ipCount, targetCount}
end
return {0, ipCount, targetCount}
`

	sendMailRateLimitReleaseScript = `
for i=1,#KEYS do
  local current = redis.call('GET', KEYS[i])
  if current then
    local num = tonumber(current)
    if num == nil or num <= 1 then
      redis.call('DEL', KEYS[i])
    else
      redis.call('DECR', KEYS[i])
    end
  end
end
return 1
`
)
