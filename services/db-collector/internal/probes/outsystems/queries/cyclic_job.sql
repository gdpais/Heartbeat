-- The timer log: one row per timer run, written from the OutSystems 11 Log data reference
-- (view oslog_Cyclic_Job); confirm the column names, types and units against
-- the real logging database before the probe review.
--
-- oslog_Cyclic_Job is a view over the current week's cycle table and
-- oslog_Cyclic_Job_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Instant is when the timer STARTED and the row is written when it ends:
-- give this log type its own lag (probes[].lag) above the timer timeout
-- (20 minutes by default). The reference documents Duration in SECONDS; it
-- is converted to milliseconds here. If the real column holds milliseconds,
-- drop the * 1000.
SELECT TOP (@limit)
    r.Instant,
    CONCAT(N'Timer ', r.Cyclic_Job_Name) AS Message,
    CASE WHEN NULLIF(r.Error_Id, '') IS NULL THEN 'INFO' ELSE 'ERROR' END AS Severity,
    r.Duration * 1000 AS Duration,
    r.Cyclic_Job_Name,
    r.Cyclic_Job_Key,
    r.Should_Have_Run_At,
    r.Next_Run,
    r.Application_Name,
    r.Espace_Name,
    r.Request_Key,
    r.Tenant_Id,
    r.Executed_By,
    NULLIF(r.Error_Id, '') AS Error_Id
FROM (
    SELECT * FROM dbo.oslog_Cyclic_Job
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_Cyclic_Job_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
