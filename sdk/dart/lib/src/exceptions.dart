/// StiQRcloud SDK Exceptions
class StiQRcloudException implements Exception {
  final String message;
  final dynamic cause;

  StiQRcloudException(this.message, [this.cause]);

  @override
  String toString() => cause != null
      ? 'StiQRcloudException: $message (Cause: $cause)'
      : 'StiQRcloudException: $message';
}

class StiQRcloudApiException extends StiQRcloudException {
  final int statusCode;
  final String responseBody;

  StiQRcloudApiException(
    this.statusCode,
    String message, [
    this.responseBody = '',
  ]) : super(message);

  @override
  String toString() =>
      'StiQRcloudApiException [$statusCode]: $message\nResponse: $responseBody';
}

class StiQRcloudNotFoundException extends StiQRcloudApiException {
  final String dataId;

  StiQRcloudNotFoundException(this.dataId, [String responseBody = ''])
      : super(404, 'Record with DataID "$dataId" not found', responseBody);
}

class StiQRcloudCryptoException extends StiQRcloudException {
  StiQRcloudCryptoException(super.message, [super.cause]);
}

// Backwards-compatible aliases
typedef TestCloudException = StiQRcloudException;
typedef TestCloudApiException = StiQRcloudApiException;
typedef TestCloudNotFoundException = StiQRcloudNotFoundException;
typedef TestCloudCryptoException = StiQRcloudCryptoException;
