-- Channel monitor probe and upstream request errors are degraded states too.
-- Normalize existing history so old failures no longer appear as error/failed.
UPDATE channel_monitor_histories
SET status = 'degraded'
WHERE status IN ('failed', 'error');
