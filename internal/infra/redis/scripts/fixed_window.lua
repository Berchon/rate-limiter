-- Atomically evaluates block state, increments the fixed-window counter,
-- heals orphan keys without TTL, and applies a temporary block when the
-- request limit is exceeded.
--
-- KEYS[1]: counter key (<prefix>:<value>:count)
-- KEYS[2]: blocked key (<prefix>:<value>:blocked)
-- ARGV[1]: maxRequests (positive integer)
-- ARGV[2]: timeWindowSec (positive integer)
-- ARGV[3]: blockedDurationSec (non-negative integer)
--
-- Returns: {allowed (0|1), remaining, resetSeconds, retryAfterSeconds}

local function pttlToSeconds(pttlMs)
	return math.max(1, math.ceil(pttlMs / 1000))
end

local function handleOrphanBlock(blockedKey, blockedDurationSec)
	if blockedDurationSec > 0 then
		redis.call("EXPIRE", blockedKey, blockedDurationSec)
		return {0, 0, blockedDurationSec, blockedDurationSec}
	end
	redis.call("DEL", blockedKey)
	return nil
end

local function checkBlockStatus(blockedKey, blockedDurationSec)
	local blockPTTL = redis.call("PTTL", blockedKey)
	if blockPTTL > 0 then
		local retryAfter = pttlToSeconds(blockPTTL)
		return {0, 0, retryAfter, retryAfter}
	end
	if blockPTTL == -1 then
		return handleOrphanBlock(blockedKey, blockedDurationSec)
	end
	return nil
end

local function incrementWindowCounter(countKey, timeWindowSec)
	local count = redis.call("INCR", countKey)
	local countPTTL = redis.call("PTTL", countKey)
	if count == 1 or countPTTL < 0 then
		redis.call("EXPIRE", countKey, timeWindowSec)
		countPTTL = timeWindowSec * 1000
	end
	return count, pttlToSeconds(countPTTL)
end

local function handleLimitExceeded(countKey, blockedKey, blockedDurationSec, resetSec)
	if blockedDurationSec > 0 then
		redis.call("SET", blockedKey, 1, "EX", blockedDurationSec)
		redis.call("DEL", countKey)
		return {0, 0, blockedDurationSec, blockedDurationSec}
	end
	return {0, 0, resetSec, resetSec}
end

local countKey = KEYS[1]
local blockedKey = KEYS[2]
local maxRequests = tonumber(ARGV[1])
local timeWindowSec = tonumber(ARGV[2])
local blockedDurationSec = tonumber(ARGV[3])

local blockedResult = checkBlockStatus(blockedKey, blockedDurationSec)
if blockedResult ~= nil then
	return blockedResult
end

local count, resetSec = incrementWindowCounter(countKey, timeWindowSec)
if count > maxRequests then
	return handleLimitExceeded(countKey, blockedKey, blockedDurationSec, resetSec)
end

return {1, maxRequests - count, resetSec, 0}
