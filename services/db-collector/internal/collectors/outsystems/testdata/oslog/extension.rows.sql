-- Representative rows for the extension log type, loaded after extension.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Synthetic rows in the surveyed schema [3.3]: rows from a real logging
-- database never reach this public repository.
INSERT INTO dbo.oslog_Extension_2 (Instant, Duration, Action_Name, Session_Id, User_Id, Espace_Id, Tenant_Id, Extension_Id, Executed_By, Error_Id, Request_Key, Espace_Name, Extension_Name, Application_Name, Application_Key, Username) VALUES
    ('2025-09-26T23:30:14.200', 1, N'RenderInvoice', 'm2n4p6q8r0s2t4u6v8w0x2y4', 1042, 17, 0, 44, N'FE-02', '',
     'a1b2c3d4-0006-4a00-8000-000000000006', N'Shop_RW', N'ExtPdf', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos');
INSERT INTO dbo.oslog_Extension_3 (Instant, Duration, Action_Name, Session_Id, User_Id, Espace_Id, Tenant_Id, Extension_Id, Executed_By, Error_Id, Request_Key, Espace_Name, Extension_Name, Application_Name, Application_Key, Username) VALUES
    ('2025-09-27T08:15:09.400', 0, N'Sign', 'k5m7n9p1q3r5s7t9u1v3w5x7', 1042, 31, 0, 45, N'FE-02', '',
     'a1b2c3d4-0004-4a00-8000-000000000004', N'Shop_IS', N'ExtCrypto', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos');
