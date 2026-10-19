-- Representative rows for the cyclic_job log type, loaded after cyclic_job.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Synthetic rows in the surveyed schema [3.3]: rows from a real logging
-- database never reach this public repository.
INSERT INTO dbo.oslog_Cyclic_Job_2 (Instant, Duration, Cyclic_Job_Key, Espace_Id, Tenant_Id, Executed_By, Error_Id, Should_Have_Run_At, Next_Run, Request_Key, Espace_Name, Application_Name, Application_Key, Cyclic_Job_Name) VALUES
    ('2025-09-26T23:43:53.000', 65, 'c0000000-0000-4000-8000-000000000001', 17, 0, N'FE-02', 'e0000000-0000-4000-8000-000000000002', '2025-09-26T23:43:00.000', '2025-09-26T23:48:00.000',
     'a1b2c3d4-0002-4a00-8000-000000000002', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'ProcessOrders');
INSERT INTO dbo.oslog_Cyclic_Job_3 (Instant, Duration, Cyclic_Job_Key, Espace_Id, Tenant_Id, Executed_By, Error_Id, Should_Have_Run_At, Next_Run, Request_Key, Espace_Name, Application_Name, Application_Key, Cyclic_Job_Name) VALUES
    ('2025-09-27T08:20:00.000', 12, 'c0000000-0000-4000-8000-000000000002', 17, 0, N'FE-01', '', '2025-09-27T08:20:00.000', '2025-09-28T08:20:00.000',
     'a1b2c3d4-0005-4a00-8000-000000000005', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'DailyReport');
