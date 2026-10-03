/// TestCloud SDK Exceptions
class TestCloudException implements Exception {
  final String message;
  final dynamic cause;

  TestCloudException(this.message, [this.cause]);

  @override
  String toString() => cause != null
      ? 'TestCloudException: $message (Cause: $cause)'
      : 'TestCloudException: $message';
}

class TestCloudApiException extends TestCloudException {
  final int statusCode;
  final String responseBody;

  TestCloudApiException(
    this.statusCode,
    String message, [
    this.responseBody = '',
  ]) : super(message);

  @override
  String toString() =>
      'TestCloudApiException [$statusCode]: $message\nResponse: $responseBody';
}

class TestCloudNotFoundException extends TestCloudApiException {
  final String dataId;

  TestCloudNotFoundException(this.dataId, [String responseBody = ''])
      : super(404, 'Record with DataID "$dataId" not found', responseBody);
}

class TestCloudCryptoException extends TestCloudException {
  TestCloudCryptoException(super.message, [super.cause]);
}
