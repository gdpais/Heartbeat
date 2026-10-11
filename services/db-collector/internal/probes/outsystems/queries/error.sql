-- Example of the query contract (see README.md), not a reviewed query:
-- replace it with the error log query you use today.
SELECT TOP (@limit)
    e.Instant,
    e.Message,
    'ERROR' AS Severity,
    e.Module_Name,
    e.Action_Name,
    e.Entrypoint_Name,
    e.Request_Key,
    e.Session_Id,
    e.User_Id,
    e.Tenant_Id,
    e.Server,
    e.Stack
FROM dbo.oslog_Error AS e
WHERE e.Instant > @from
  AND e.Instant <= @to
ORDER BY e.Instant;
