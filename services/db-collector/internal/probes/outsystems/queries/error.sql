-- The error log: one row per logged exception, written from the OutSystems 11 Log data reference
-- (view oslog_Error); confirm the column names, types and units against
-- the real logging database before the probe review.
--
-- oslog_Error is a view over the current week's cycle table and
-- oslog_Error_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- The Error view spells the key Request_key; it is aliased so the attribute
-- is Request_Key on every log type.
SELECT TOP (@limit)
    r.Instant,
    r.Message,
    'ERROR' AS Severity,
    r.Application_Name,
    r.Espace_Name,
    r.Module_Name,
    r.Entrypoint_Name,
    r.Action_Name,
    r.Request_key AS Request_Key,
    r.Session_Id,
    r.User_Id,
    r.Username,
    r.Tenant_Id,
    r.Server,
    r.Stack,
    r.Environment_Information
FROM (
    SELECT * FROM dbo.oslog_Error
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_Error_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
