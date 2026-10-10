-- SQL Server fixture for the error log type: the oslog tables
-- queries/error.sql reads. The SQL Server tests create them in an empty
-- database and grant the test login SELECT on each table, nothing else.
--
-- A stand-in with the columns error.sql selects, typed like OutSystems 11
-- (Instant is datetime). Replace it with the real logging database DDL
-- (TODO 3.3, SQL Server fixture).
CREATE TABLE dbo.oslog_Error (
    Id bigint IDENTITY(1, 1) NOT NULL,
    Instant datetime NOT NULL,
    Message nvarchar(2000) NULL,
    Module_Name nvarchar(50) NULL,
    Action_Name nvarchar(100) NULL,
    Entrypoint_Name nvarchar(100) NULL,
    Request_Key char(36) NULL,
    Session_Id varchar(50) NULL,
    User_Id int NULL,
    Tenant_Id int NULL,
    Server nvarchar(50) NULL,
    Stack nvarchar(max) NULL,
    CONSTRAINT PK_oslog_Error PRIMARY KEY NONCLUSTERED (Id)
);
CREATE CLUSTERED INDEX IX_oslog_Error_Instant ON dbo.oslog_Error (Instant);
