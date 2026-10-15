import 'dart:convert';
import 'package:http/http.dart' as http;
import 'exceptions.dart';
import 'models.dart';

/// NodeManager allows developers and apps to add, prioritize, and failover across multiple nodes.
class NodeManager {
  final Map<String, NodeConfig> _nodes = {};
  String _primaryUrl;
  final bool autoFallback;
  final http.Client _client;

  NodeManager({
    String? primaryUrl,
    List<dynamic>? nodes,
    this.autoFallback = true,
    http.Client? httpClient,
  })  : _primaryUrl = normalizeUrl(primaryUrl ?? 'http://localhost:8100'),
        _client = httpClient ?? http.Client() {
    // Add primary node
    addNode(_primaryUrl, isPrimary: true, name: 'Primary Node');

    if (nodes != null) {
      for (final n in nodes) {
        if (n is String) {
          addNode(n);
        } else if (n is NodeConfig) {
          addNode(n.url, name: n.name, isPrimary: n.isPrimary);
        }
      }
    }
  }

  static String normalizeUrl(String url) {
    var trimmed = url.trim().replaceAll(RegExp(r'/+$'), '');
    if (!trimmed.startsWith('http://') && !trimmed.startsWith('https://')) {
      trimmed = 'http://$trimmed';
    }
    return trimmed;
  }

  String get primaryUrl => _primaryUrl;

  List<NodeConfig> get nodes => _nodes.values.toList();

  List<NodeConfig> listNodes() => _nodes.values.toList();

  NodeConfig addNode(
    dynamic node, {
    String? name,
    bool isPrimary = false,
  }) {
    String rawUrl;
    if (node is NodeConfig) {
      rawUrl = node.url;
      name = name ?? node.name;
      isPrimary = isPrimary || node.isPrimary;
    } else {
      rawUrl = node.toString();
    }
    final norm = normalizeUrl(rawUrl);
    if (isPrimary) {
      _primaryUrl = norm;
    }
    final config = NodeConfig(
      url: norm,
      name: name ?? norm,
      isPrimary: isPrimary,
      isHealthy: true,
    );
    _nodes[norm] = config;
    return config;
  }

  bool removeNode(String url) {
    final norm = normalizeUrl(url);
    if (_primaryUrl == norm) {
      final remaining = _nodes.keys.where((k) => k != norm).toList();
      if (remaining.isNotEmpty) {
        _primaryUrl = remaining.first;
      }
    }
    return _nodes.remove(norm) != null;
  }

  void setPrimaryNode(String url) {
    final norm = normalizeUrl(url);
    _primaryUrl = norm;
    if (!_nodes.containsKey(norm)) {
      addNode(norm, isPrimary: true);
    }
  }

  /// Probes the health of a specific node URL
  Future<NodeHealth> checkHealth(String url) async {
    final norm = normalizeUrl(url);
    final uri = Uri.parse('$norm/api/health');
    try {
      final resp = await _client.get(uri).timeout(const Duration(seconds: 4));
      if (resp.statusCode != 200) {
        throw TestCloudException('Node health check returned ${resp.statusCode}: ${resp.body}');
      }
      final map = jsonDecode(resp.body) as Map<String, dynamic>;
      final health = NodeHealth.fromJson(map);
      _nodes[norm] = NodeConfig(
        url: norm,
        name: _nodes[norm]?.name ?? health.nodeId,
        isPrimary: _primaryUrl == norm,
        isHealthy: true,
        lastChecked: DateTime.now().toUtc(),
      );
      return health;
    } catch (e) {
      _nodes[norm] = NodeConfig(
        url: norm,
        name: _nodes[norm]?.name,
        isPrimary: _primaryUrl == norm,
        isHealthy: false,
        lastChecked: DateTime.now().toUtc(),
      );
      throw TestCloudException('Failed to reach node at $norm: $e');
    }
  }

  /// Resolves an active, reachable node endpoint.
  /// If [explicitUrl] is provided, uses that endpoint directly.
  /// If primary node fails and [autoFallback] is true, checks fallback nodes.
  Future<String> resolveEndpoint([String? explicitUrl]) async {
    if (explicitUrl != null && explicitUrl.isNotEmpty) {
      return normalizeUrl(explicitUrl);
    }

    final target = _primaryUrl;
    if (!autoFallback) return target;

    // Check if primary is healthy
    try {
      await checkHealth(target);
      return target;
    } catch (_) {
      // Fallback to remaining registered nodes
      for (final n in _nodes.values) {
        if (n.url != target) {
          try {
            await checkHealth(n.url);
            return n.url;
          } catch (_) {
            continue;
          }
        }
      }
      // If all fails, return primary and let caller handle network error
      return target;
    }
  }
}
