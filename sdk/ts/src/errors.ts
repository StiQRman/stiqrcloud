/**
 * StiQRcloud SDK Error Classes
 */

export class StiQRcloudError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'StiQRcloudError';
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class StiQRcloudApiException extends StiQRcloudError {
  public readonly statusCode: number;
  public readonly responseBody: string;

  constructor(statusCode: number, message: string, responseBody: string) {
    super(`${message} (Status ${statusCode}): ${responseBody}`);
    this.name = 'StiQRcloudApiException';
    this.statusCode = statusCode;
    this.responseBody = responseBody;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class StiQRcloudNotFoundError extends StiQRcloudError {
  public readonly dataId: string;

  constructor(dataId: string, responseBody?: string) {
    super(`Record with data_id "${dataId}" was not found.${responseBody ? ` Details: ${responseBody}` : ''}`);
    this.name = 'StiQRcloudNotFoundError';
    this.dataId = dataId;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class StiQRcloudCryptoError extends StiQRcloudError {
  constructor(message: string) {
    super(`StiQRcloud Cryptography Error: ${message}`);
    this.name = 'StiQRcloudCryptoError';
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class StiQRcloudNodeOfflineError extends StiQRcloudError {
  public readonly nodeUrl: string;

  constructor(nodeUrl: string, cause?: any) {
    super(`StiQRcloud Node at "${nodeUrl}" is unreachable or offline.${cause ? ` Cause: ${cause.message || cause}` : ''}`);
    this.name = 'StiQRcloudNodeOfflineError';
    this.nodeUrl = nodeUrl;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

// Backwards-compatible aliases
export const TestCloudError = StiQRcloudError;
export type TestCloudError = StiQRcloudError;

export const TestCloudApiException = StiQRcloudApiException;
export type TestCloudApiException = StiQRcloudApiException;

export const TestCloudNotFoundError = StiQRcloudNotFoundError;
export type TestCloudNotFoundError = StiQRcloudNotFoundError;

export const TestCloudCryptoError = StiQRcloudCryptoError;
export type TestCloudCryptoError = StiQRcloudCryptoError;

export const TestCloudNodeOfflineError = StiQRcloudNodeOfflineError;
export type TestCloudNodeOfflineError = StiQRcloudNodeOfflineError;
