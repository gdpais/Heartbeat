-- Representative rows for the error log type, loaded after error.sql by
-- make test-sqlserver. The query must return every row inserted here, from
-- the previous week's table as well as the current week's, so there are rows
-- on both sides of the Friday 2025-09-26 23:45 rotation. The Request_Key
-- values are shared with the other log types' rows: one request appears
-- across screen, integration, extension, error and general.
--
-- Stand-in rows until rows copied from the logging database replace them
-- (TODO 3.3); copy them as they are, user ids, sessions and IPs included.
INSERT INTO dbo.oslog_Error_2 (Id, Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Message, Stack, Module_Name, Server, Environment_Information, Request_key, Entrypoint_Name, Action_Name, Espace_Name, Application_Name, Application_Key, Username) VALUES
    ('e0000000-0000-4000-8000-000000000001', '2025-09-26T23:41:12.357', 'u7k2v4zz5qd3m9n1p8r6s0t2', 1042, 17, 0,
     N'Object reference not set to an instance of an object.',
     N'at ssShop_CS.Flows.FlowCheckout.ScrCheckout.CommitTransaction()\r\nat ssShop_CS.Flows.FlowCheckout.ScrCheckout.btnPlaceOrder_Click()',
     N'Shop_CS', N'FE-01', N'eSpaceVer: 412 (Id=17, PubId=903), RequestUrl: https://shop.example.internal/Shop_CS/Checkout.aspx',
     'a1b2c3d4-0001-4a00-8000-000000000001', N'Checkout', N'PlaceOrder', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos'),
    ('e0000000-0000-4000-8000-000000000002', '2025-09-26T23:44:58.997', NULL, NULL, 17, 0,
     N'Timer ProcessOrders: Execution Timeout Expired. The timeout period elapsed prior to completion of the operation.',
     N'at ssShop_CS.Actions.ActionProcessOrders()', N'Shop_CS', N'FE-02', NULL,
     'a1b2c3d4-0002-4a00-8000-000000000002', N'ProcessOrders', N'ProcessOrders', N'Shop_CS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', NULL);
INSERT INTO dbo.oslog_Error_3 (Id, Instant, Session_Id, User_Id, Espace_Id, Tenant_Id, Message, Stack, Module_Name, Server, Environment_Information, Request_key, Entrypoint_Name, Action_Name, Espace_Name, Application_Name, Application_Key, Username) VALUES
    ('e0000000-0000-4000-8000-000000000003', '2025-09-27T00:03:05.123', 'w3x9y1z5a7b2c4d6e8f0g2h4', 2077, 23, 0,
     N'Invalid login attempt.', N'at ssUsers.Flows.FlowLogin.ScrLogin.DoLogin()', N'Users', N'FE-01', NULL,
     'a1b2c3d4-0003-4a00-8000-000000000003', N'Login', N'DoLogin', N'Users', N'Users', 'b1c2d3e4-f5a6-4b7c-9d8e-0f1a2b3c4d5e', N'joao.pereira'),
    ('e0000000-0000-4000-8000-000000000005', '2025-09-27T00:03:05.123', 'w3x9y1z5a7b2c4d6e8f0g2h4', 2077, 23, 0,
     N'Invalid login attempt.', N'at ssUsers.Flows.FlowLogin.ScrLogin.DoLogin()', N'Users', N'FE-01', NULL,
     'a1b2c3d4-0003-4a00-8000-000000000003', N'Login', N'DoLogin', N'Users', N'Users', 'b1c2d3e4-f5a6-4b7c-9d8e-0f1a2b3c4d5e', N'joao.pereira'),
    ('e0000000-0000-4000-8000-000000000004', '2025-09-27T08:15:40.000', 'k5m7n9p1q3r5s7t9u1v3w5x7', 1042, 31, 0,
     N'The remote server returned an error: (503) Service Unavailable.',
     N'at ssShop_IS.Actions.Payments_Charge()\r\nat ssShop_RW.Actions.ActionPay()', N'Shop_IS', N'FE-02', NULL,
     'a1b2c3d4-0004-4a00-8000-000000000004', N'Checkout', N'Pay', N'Shop_IS', N'Shop', 'a0b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d', N'maria.santos');
