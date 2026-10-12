-- The integration log: REST, SOAP and SAP calls, consumed and exposed, written from the OutSystems 11 Log data reference
-- (view oslog_Integration); confirm the column names, types and units against
-- the real logging database before the probe review.
--
-- oslog_Integration is a view over the current week's cycle table and
-- oslog_Integration_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- Columns that differ between the log views are returned under the standard
-- names of README.md (Standard columns).
-- Instant is when the call COMPLETED. Duration is milliseconds. A row with
-- an Error_Id is ERROR. Source is the caller of an exposed API (standard
-- name Client_IP) and empty for consumed calls; Endpoint is the consumed
-- URL and empty for exposed ones.
SELECT TOP (@limit)
    r.Instant,
    CONCAT(r.Type, N' ', r.Action, N' ', r.Endpoint) AS Message,
    CASE WHEN NULLIF(r.Error_Id, '') IS NULL THEN 'INFO' ELSE 'ERROR' END AS Severity,
    r.Duration,
    r.Action AS Action_Name,
    r.Endpoint,
    r.Type,
    r.Is_Expose,
    r.Application_Name,
    r.Espace_Name,
    r.Request_Key,
    r.Tenant_Id,
    NULLIF(r.Source, '') AS Client_IP,
    r.Executed_By AS Server,
    NULLIF(r.Error_Id, '') AS Error_Id
FROM (
    SELECT * FROM dbo.oslog_Integration
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_Integration_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
