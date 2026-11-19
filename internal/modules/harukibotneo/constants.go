package harukibotneo

import "time"

const (
	// botMailDisplayName is the sender name of HarukiBot NEO code mail.
	botMailDisplayName = "Haruki Bot | 星云科技"

	verifyCodeTTL     = 10 * time.Minute
	verifyCodeLen     = 6
	maxVerifyAttempts = 5

	sendMailRateLimitWindow = 60 * time.Minute
	sendMailIPLimit         = 20
	sendMailTargetLimit     = 5

	registerRateLimitWindow = 10 * time.Minute
	registerActorLimit      = 5

	botIDMin     = 10000000
	botIDMax     = 99999999
	botIDRetries = 10

	credentialBytes = 32

	rateLimitLimitedByNone   = int64(0)
	rateLimitLimitedByIP     = int64(1)
	rateLimitLimitedByTarget = int64(2)

	sendMailRateLimitScript = `
local ipCount = tonumber(redis.call('GET', KEYS[1]) or '0')
local targetCount = tonumber(redis.call('GET', KEYS[2]) or '0')
if ipCount >= tonumber(ARGV[1]) then
  return {1, ipCount, targetCount}
end
if targetCount >= tonumber(ARGV[2]) then
  return {2, ipCount, targetCount}
end
local function reserve(key)
  local count = redis.call('INCR', key)
  if count == 1 then redis.call('PEXPIRE', key, ARGV[3]) end
  return count
end
return {0, reserve(KEYS[1]), reserve(KEYS[2])}
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
