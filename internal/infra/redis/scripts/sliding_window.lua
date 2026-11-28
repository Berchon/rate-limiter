-- Atomically evaluates block state, prunes expired entries from the sliding-window
-- log (ZSET), checks capacity, heals orphan keys without TTL, and applies a
-- temporary block when the request limit is exceeded.
--
-- KEYS[1]: sliding-window log key (<prefix>:<value>:log)
-- KEYS[2]: blocked key (<prefix>:<value>:blocked)
-- ARGV[1]: maxRequests (positive integer)
-- ARGV[2]: timeWindowSec (positive integer)
-- ARGV[3]: blockedDurationSec (non-negative integer)
-- ARGV[4]: nowMs (current unix timestamp in milliseconds)
-- ARGV[5]: member (unique identifier for this request in the sorted set)
--
-- Returns: {allowed (0|1), remaining, resetSeconds, retryAfterSeconds}

local function msToSeconds(ms)
	return math.max(1, math.ceil(ms / 1000))
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
		local retryAfter = msToSeconds(blockPTTL)
		return {0, 0, retryAfter, retryAfter}
	end
	if blockPTTL == -1 then
		return handleOrphanBlock(blockedKey, blockedDurationSec)
	end
	return nil
end

local function calculateResetSeconds(logKey, nowMs, windowMs)
	local oldest = redis.call("ZRANGE", logKey, 0, 0, "WITHSCORES")
	if #oldest < 2 then
		return msToSeconds(windowMs)
	end
	local oldestMs = tonumber(oldest[2])
	local remainingMs = (oldestMs + windowMs) - nowMs
	if remainingMs > windowMs then
		remainingMs = windowMs
	end
	return msToSeconds(remainingMs)
end

local function handleLimitExceeded(logKey, blockedKey, blockedDurationSec, resetSec, windowMs)
	if blockedDurationSec > 0 then
		redis.call("SET", blockedKey, 1, "EX", blockedDurationSec)
		redis.call("DEL", logKey)
		return {0, 0, blockedDurationSec, blockedDurationSec}
	end
	if redis.call("PTTL", logKey) < 0 then
		redis.call("PEXPIRE", logKey, windowMs)
	end
	return {0, 0, resetSec, resetSec}
end

local logKey = KEYS[1]
local blockedKey = KEYS[2]
local maxRequests = tonumber(ARGV[1])
local timeWindowSec = tonumber(ARGV[2])
local blockedDurationSec = tonumber(ARGV[3])
local nowMs = tonumber(ARGV[4])
local member = ARGV[5]
local windowMs = timeWindowSec * 1000

local blockedResult = checkBlockStatus(blockedKey, blockedDurationSec)
if blockedResult ~= nil then
	return blockedResult
end

local windowStartMs = nowMs - windowMs
redis.call("ZREMRANGEBYSCORE", logKey, "-inf", windowStartMs)

local count = redis.call("ZCARD", logKey)
if count >= maxRequests then
	local resetSec = calculateResetSeconds(logKey, nowMs, windowMs)
	return handleLimitExceeded(logKey, blockedKey, blockedDurationSec, resetSec, windowMs)
end

redis.call("ZADD", logKey, nowMs, member)
redis.call("PEXPIRE", logKey, windowMs)

local resetSec = calculateResetSeconds(logKey, nowMs, windowMs)
return {1, maxRequests - (count + 1), resetSec, 0}
