-- The timer log: one row per timer run, written from the OutSystems 11 Log data reference
-- (view oslog_Cyclic_Job); column names and types checked against the logging-database
-- survey [3.3].
--
-- oslog_Cyclic_Job is a view over the current week's cycle table and
-- oslog_Cyclic_Job_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- Instant is when the timer STARTED and the row is written when it ends:
-- give this log type its own lag (probes[].lag) above the timer timeout
-- (20 minutes by default). The reference documents Duration in SECONDS; it
-- is converted to milliseconds here. If the real column holds milliseconds,
-- drop the * 1000. The timer's name is the operation (Action_Name).
SELECT TOP (@limit)
    r.Instant,
    CONCAT(N'Timer ', r.Cyclic_Job_Name) AS Message,
    CASE WHEN NULLIF(r.Error_Id, '') IS NULL THEN 'INFO' ELSE 'ERROR' END AS Severity,
    r.Duration * 1000 AS Duration,
    r.Cyclic_Job_Name AS Action_Name,
    r.Cyclic_Job_Key,
    r.Should_Have_Run_At,
    r.Next_Run,
    r.Application_Name,
    r.Espace_Name,
    r.Request_Key,
    r.Tenant_Id,
    r.Executed_By AS Server,
    NULLIF(r.Error_Id, '') AS Error_Id
-- Each week's view is cut to @limit rows in Instant order before the
-- union, so a backlog costs @limit index rows per view instead of every row
-- in (@from, @to]: the first @limit rows overall are among them.
FROM (
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_Cyclic_Job
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS current_week
    UNION ALL
    SELECT * FROM (
        SELECT TOP (@limit) * FROM dbo.oslog_Cyclic_Job_Previous
        WHERE Instant > @from AND Instant <= @to
        ORDER BY Instant
    ) AS previous_week
) AS r
ORDER BY r.Instant;
