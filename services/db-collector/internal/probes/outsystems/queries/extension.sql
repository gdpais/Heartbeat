-- The extension log: Integration Studio extension actions, written from the OutSystems 11 Log data reference
-- (view oslog_Extension); confirm the column names, types and units against
-- the real logging database before the probe review.
--
-- oslog_Extension is a view over the current week's cycle table and
-- oslog_Extension_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- The reference documents Duration in SECONDS for this log; it is converted
-- to milliseconds here. If the real column holds milliseconds, drop the
-- * 1000.
SELECT TOP (@limit)
    r.Instant,
    CONCAT(r.Extension_Name, N'.', r.Action_Name) AS Message,
    CASE WHEN NULLIF(r.Error_Id, '') IS NULL THEN 'INFO' ELSE 'ERROR' END AS Severity,
    r.Duration * 1000 AS Duration,
    r.Action_Name,
    r.Extension_Name,
    r.Application_Name,
    r.Espace_Name,
    r.Request_Key,
    r.Session_Id,
    r.User_Id,
    r.Username,
    r.Tenant_Id,
    r.Executed_By AS Server,
    NULLIF(r.Error_Id, '') AS Error_Id
FROM (
    SELECT * FROM dbo.oslog_Extension
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_Extension_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
