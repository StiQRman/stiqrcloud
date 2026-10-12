/**
 * TestCloud SDK Error Classes
 */

export class TestCloudError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TestCloudError';
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class TestCloudApiException extends TestCloudError {
  public readonly statusCode: number;
  public readonly responseBody: string;

  constructor(statusCode: number, message: string, responseBody: string) {
    super(`${message} (Status ${statusCode}): ${responseBody}`);
    this.name = 'TestCloudApiException';
    this.statusCode = statusCode;
    this.responseBody = responseBody;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class TestCloudNotFoundError extends TestCloudError {
  public readonly dataId: string;

  constructor(dataId: string, responseBody?: string) {
    super(`Record with data_id "${dataId}" was not found.${responseBody ? ` Details: ${responseBody}` : ''}`);
    this.name = 'TestCloudNotFoundError';
    this.dataId = dataId;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class TestCloudCryptoError extends TestCloudError {
  constructor(message: string) {
    super(`TestCloud Cryptography Error: ${message}`);
    this.name = 'TestCloudCryptoError';
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class TestCloudNodeOfflineError extends TestCloudError {
  public readonly nodeUrl: string;

  constructor(nodeUrl: string, cause?: any) {
    super(`TestCloud Node at "${nodeUrl}" is unreachable or offline.${cause ? ` Cause: ${cause.message || cause}` : ''}`);
    this.name = 'TestCloudNodeOfflineError';
    this.nodeUrl = nodeUrl;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}
