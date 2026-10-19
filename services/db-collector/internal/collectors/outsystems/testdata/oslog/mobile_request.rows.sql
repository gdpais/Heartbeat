-- Representative rows for the mobile_request log type, loaded after mobile_request.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Synthetic rows in the surveyed schema [3.3]: rows from a real logging
-- database never reach this public repository.
INSERT INTO dbo.oslog_mobile_request_2 (Tenant_Id, Id, Instant, Espace_Id, Screen, Source, Endpoint, Duration, Executed_By, Error_Id, Request_Key, Login_Id, User_Id, Espace_Name, Application_Name, Application_Key, Username) VALUES
    (0, 'm0000000-0000-4000-8000-000000000001', '2025-09-26T23:30:15.600', 29, N'Orders', N'10.20.30.41', N'GetOrders', 1, N'FE-02', '',
     'a1b2c3d4-0006-4a00-8000-000000000006', 'l9a8b7c6d5e4f3g2h1i0j9k8', 1042, N'Shop_RW', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos');
INSERT INTO dbo.oslog_mobile_request_3 (Tenant_Id, Id, Instant, Espace_Id, Screen, Source, Endpoint, Duration, Executed_By, Error_Id, Request_Key, Login_Id, User_Id, Espace_Name, Application_Name, Application_Key, Username) VALUES
    (0, 'm0000000-0000-4000-8000-000000000002', '2025-09-27T08:15:40.050', 29, N'Checkout', N'10.20.30.41', N'Pay', 31, N'FE-02', 'e0000000-0000-4000-8000-000000000004',
     'a1b2c3d4-0004-4a00-8000-000000000004', 'l9a8b7c6d5e4f3g2h1i0j9k8', 1042, N'Shop_RW', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos');
