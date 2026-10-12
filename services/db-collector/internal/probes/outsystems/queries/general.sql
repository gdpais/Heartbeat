-- The general log: messages apps write with LogMessage, written from the OutSystems 11 Log data reference
-- (view oslog_General); confirm the column names, types and units against
-- the real logging database before the probe review.
--
-- oslog_General is a view over the current week's cycle table and
-- oslog_General_Previous over last week's; the platform switches them every
-- Friday at 23:45 (database time). Reading both keeps the rows logged just
-- before a rotation and lets initial_lookback reach back a week.
-- No Severity column: every row is INFO. Message_Type is the free text the
-- app passed as the module name. An empty Error_Id is dropped (NULL).
SELECT TOP (@limit)
    r.Instant,
    r.Message,
    r.Message_Type,
    r.Application_Name,
    r.Espace_Name,
    r.Module_Name,
    r.Entrypoint_Name,
    r.Action_Name,
    r.Request_Key,
    r.Session_Id,
    r.User_Id,
    r.Username,
    r.Tenant_Id,
    r.Client_IP,
    NULLIF(r.Error_Id, '') AS Error_Id
FROM (
    SELECT * FROM dbo.oslog_General
    WHERE Instant > @from AND Instant <= @to
    UNION ALL
    SELECT * FROM dbo.oslog_General_Previous
    WHERE Instant > @from AND Instant <= @to
) AS r
ORDER BY r.Instant;
