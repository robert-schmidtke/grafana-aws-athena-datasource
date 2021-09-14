import { DataQuery, DataSourceJsonData } from '@grafana/data';

export interface AwsAthenaOptions extends DataSourceJsonData {
  defaultRegion: string;
  defaultWorkgroup: string;
  profile: string;
  assumeRoleArn?: string;
  outputLocation: string;
  queryTimeout: string;
}

export interface AwsAthenaSecureJsonData {
  accessKey: string;
  secretKey: string;
}

export enum AwsAuthType {
  KEY = 'keys',
  CREDENTIALS = 'credentials',
  ARN = 'arn',
}

export interface AwsAthenaQuery extends DataQuery {
  refId: string;
  region: string;
  workgroup: string;
  queryExecutionId: string;
  inputs: any;
  timestampColumn: string;
  valueColumn: string;
  legendFormat: string;
  timeFormat: string;
  maxRows: string;
  cacheDuration: string;
  queryString: string;
  outputLocation: string;
}
