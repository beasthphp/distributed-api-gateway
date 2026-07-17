#include <algorithm>
#include <atomic>
#include <chrono>
#include <cmath>
#include <condition_variable>
#include <cstdlib>
#include <curl/curl.h>
#include <filesystem>
#include <fstream>
#include <iomanip>
#include <iostream>
#include <map>
#include <mutex>
#include <numeric>
#include <sstream>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

namespace {

struct Options {
    std::string url = "http://127.0.0.1:8080/api/users";
    std::string output = "-";
    std::string scenario = "manual-cpp";
    int requests = 2000;
    int concurrency = 32;
    int warmup = 100;
    int instances = 1;
    int timeout_ms = 5000;
    int minimum_upstreams = 0;
};

struct Sample {
    long long latency_us = 0;
    long status_code = 0;
    bool transport_error = false;
    bool rate_limit_headers_valid = false;
    std::string upstream;
};

struct ResponseHeaders {
    bool limit = false;
    bool remaining = false;
    bool reset = false;
    bool retry_after = false;
    std::string upstream;
};

std::string require_value(int& index, int argc, char** argv) {
    if (++index >= argc) {
        throw std::runtime_error(std::string("missing value for ") + argv[index - 1]);
    }
    return argv[index];
}

int positive_integer(const std::string& raw, const std::string& name, bool allow_zero = false) {
    std::size_t consumed = 0;
    int value = 0;
    try {
        value = std::stoi(raw, &consumed);
    } catch (const std::exception&) {
        throw std::runtime_error(name + " must be an integer");
    }
    if (consumed != raw.size() || value < (allow_zero ? 0 : 1)) {
        throw std::runtime_error(name + (allow_zero ? " must be non-negative" : " must be positive"));
    }
    return value;
}

Options parse_options(int argc, char** argv) {
    Options options;
    for (int index = 1; index < argc; ++index) {
        const std::string argument = argv[index];
        if (argument == "--url") {
            options.url = require_value(index, argc, argv);
        } else if (argument == "--output") {
            options.output = require_value(index, argc, argv);
        } else if (argument == "--scenario") {
            options.scenario = require_value(index, argc, argv);
        } else if (argument == "--requests") {
            options.requests = positive_integer(require_value(index, argc, argv), "requests");
        } else if (argument == "--concurrency") {
            options.concurrency = positive_integer(require_value(index, argc, argv), "concurrency");
        } else if (argument == "--warmup") {
            options.warmup = positive_integer(require_value(index, argc, argv), "warmup", true);
        } else if (argument == "--instances") {
            options.instances = positive_integer(require_value(index, argc, argv), "instances");
        } else if (argument == "--timeout-ms") {
            options.timeout_ms = positive_integer(require_value(index, argc, argv), "timeout-ms");
        } else if (argument == "--min-upstreams") {
            options.minimum_upstreams = positive_integer(require_value(index, argc, argv), "min-upstreams", true);
        } else {
            throw std::runtime_error("unknown argument: " + argument);
        }
    }
    return options;
}

std::string trim(std::string value) {
    const auto first = value.find_first_not_of(" \t\r\n");
    if (first == std::string::npos) {
        return "";
    }
    const auto last = value.find_last_not_of(" \t\r\n");
    return value.substr(first, last - first + 1);
}

std::string lowercase(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char character) {
        if (character >= 'A' && character <= 'Z') {
            return static_cast<char>(character - 'A' + 'a');
        }
        return static_cast<char>(character);
    });
    return value;
}

std::size_t discard_body(char*, std::size_t size, std::size_t count, void*) {
    return size * count;
}

std::size_t capture_header(char* buffer, std::size_t size, std::size_t count, void* userdata) {
    const std::size_t length = size * count;
    auto* headers = static_cast<ResponseHeaders*>(userdata);
    std::string line(buffer, length);
    const auto separator = line.find(':');
    if (separator == std::string::npos) {
        return length;
    }
    const std::string name = lowercase(trim(line.substr(0, separator)));
    const std::string value = trim(line.substr(separator + 1));
    if (name == "x-ratelimit-limit") {
        headers->limit = true;
    } else if (name == "x-ratelimit-remaining") {
        headers->remaining = true;
    } else if (name == "x-ratelimit-reset") {
        headers->reset = true;
    } else if (name == "retry-after") {
        headers->retry_after = true;
    } else if (name == "x-benchmark-upstream") {
        headers->upstream = value;
    }
    return length;
}

class CurlWorker {
  public:
    CurlWorker(const Options& options, const std::string& api_key) : handle_(curl_easy_init()) {
        if (handle_ == nullptr) {
            throw std::runtime_error("curl_easy_init failed");
        }
        header_list_ = curl_slist_append(header_list_, ("X-API-Key: " + api_key).c_str());
        header_list_ = curl_slist_append(header_list_, "Accept: application/json");
        if (header_list_ == nullptr) {
            throw std::runtime_error("curl_slist_append failed");
        }
        curl_easy_setopt(handle_, CURLOPT_URL, options.url.c_str());
        curl_easy_setopt(handle_, CURLOPT_HTTPHEADER, header_list_);
        curl_easy_setopt(handle_, CURLOPT_WRITEFUNCTION, discard_body);
        curl_easy_setopt(handle_, CURLOPT_HEADERFUNCTION, capture_header);
        curl_easy_setopt(handle_, CURLOPT_TIMEOUT_MS, static_cast<long>(options.timeout_ms));
        curl_easy_setopt(handle_, CURLOPT_NOSIGNAL, 1L);
        curl_easy_setopt(handle_, CURLOPT_TCP_KEEPALIVE, 1L);
        curl_easy_setopt(handle_, CURLOPT_FOLLOWLOCATION, 0L);
        curl_easy_setopt(handle_, CURLOPT_ACCEPT_ENCODING, "identity");
    }

    CurlWorker(const CurlWorker&) = delete;
    CurlWorker& operator=(const CurlWorker&) = delete;

    ~CurlWorker() {
        if (header_list_ != nullptr) {
            curl_slist_free_all(header_list_);
        }
        if (handle_ != nullptr) {
            curl_easy_cleanup(handle_);
        }
    }

    Sample request() {
        ResponseHeaders headers;
        curl_easy_setopt(handle_, CURLOPT_HEADERDATA, &headers);
        const auto started = std::chrono::steady_clock::now();
        const CURLcode code = curl_easy_perform(handle_);
        const auto elapsed = std::chrono::duration_cast<std::chrono::microseconds>(
            std::chrono::steady_clock::now() - started);
        Sample sample;
        sample.latency_us = elapsed.count();
        if (code != CURLE_OK) {
            sample.transport_error = true;
            return sample;
        }
        curl_easy_getinfo(handle_, CURLINFO_RESPONSE_CODE, &sample.status_code);
        sample.rate_limit_headers_valid = headers.limit && headers.remaining && headers.reset;
        if (sample.status_code == 429) {
            sample.rate_limit_headers_valid = sample.rate_limit_headers_valid && headers.retry_after;
        }
        sample.upstream = headers.upstream;
        return sample;
    }

  private:
    CURL* handle_ = nullptr;
    curl_slist* header_list_ = nullptr;
};

long long nearest_rank(std::vector<long long> sorted, double quantile) {
    if (sorted.empty()) {
        return 0;
    }
    std::sort(sorted.begin(), sorted.end());
    std::size_t index = static_cast<std::size_t>(std::ceil(quantile * static_cast<double>(sorted.size()))) - 1;
    if (index >= sorted.size()) {
        index = sorted.size() - 1;
    }
    return sorted[index];
}

std::string json_escape(const std::string& value) {
    std::ostringstream escaped;
    for (const unsigned char character : value) {
        switch (character) {
            case '"': escaped << "\\\""; break;
            case '\\': escaped << "\\\\"; break;
            case '\b': escaped << "\\b"; break;
            case '\f': escaped << "\\f"; break;
            case '\n': escaped << "\\n"; break;
            case '\r': escaped << "\\r"; break;
            case '\t': escaped << "\\t"; break;
            default:
                if (character < 0x20) {
                    escaped << "\\u" << std::hex << std::setw(4) << std::setfill('0')
                            << static_cast<int>(character) << std::dec;
                } else {
                    escaped << static_cast<char>(character);
                }
        }
    }
    return escaped.str();
}

std::string started_at_utc(std::chrono::system_clock::time_point value) {
    const std::time_t timestamp = std::chrono::system_clock::to_time_t(value);
    std::tm utc{};
    gmtime_r(&timestamp, &utc);
    std::ostringstream formatted;
    formatted << std::put_time(&utc, "%Y-%m-%dT%H:%M:%SZ");
    return formatted.str();
}

std::string architecture() {
#if defined(__x86_64__)
    return "amd64";
#elif defined(__aarch64__)
    return "arm64";
#else
    return "unknown";
#endif
}

void write_string_map(std::ostream& output, const std::map<std::string, int>& values, int indent) {
    output << "{";
    bool first = true;
    for (const auto& [key, value] : values) {
        if (!first) {
            output << ",";
        }
        output << "\n" << std::string(indent, ' ') << "\"" << json_escape(key) << "\": " << value;
        first = false;
    }
    if (!values.empty()) {
        output << "\n" << std::string(indent - 2, ' ');
    }
    output << "}";
}

bool write_result(const Options& options, const std::vector<Sample>& samples,
                  std::chrono::system_clock::time_point started_at,
                  std::chrono::steady_clock::duration duration) {
    std::vector<long long> latencies;
    latencies.reserve(samples.size());
    std::map<std::string, int> statuses;
    std::map<std::string, int> upstreams;
    int transport_errors = 0;
    int http_errors = 0;
    int rate_limited = 0;
    long double latency_total = 0;
    for (const Sample& sample : samples) {
        latencies.push_back(sample.latency_us);
        latency_total += static_cast<long double>(sample.latency_us);
        if (sample.transport_error) {
            ++transport_errors;
        } else {
            ++statuses[std::to_string(sample.status_code)];
        }
        if (sample.status_code >= 400) {
            ++http_errors;
        }
        if (sample.status_code == 429) {
            ++rate_limited;
        }
        if (!sample.upstream.empty()) {
            ++upstreams[sample.upstream];
        }
    }
    const double duration_ms = std::chrono::duration<double, std::milli>(duration).count();
    const double throughput = static_cast<double>(samples.size()) / (duration_ms / 1000.0);
    const double mean_ms = static_cast<double>(latency_total / static_cast<long double>(samples.size())) / 1000.0;
    const double error_rate = 100.0 * static_cast<double>(transport_errors + http_errors) /
                              static_cast<double>(samples.size());
    const bool statuses_allowed = statuses.size() == 1 && statuses.count("200") == 1;
    const bool upstreams_observed = options.minimum_upstreams == 0 ||
                                    static_cast<int>(upstreams.size()) >= options.minimum_upstreams;
    const bool passed = transport_errors == 0 && statuses_allowed && upstreams_observed;

    std::ofstream file;
    std::ostream* output = &std::cout;
    if (options.output != "-") {
        const std::filesystem::path path(options.output);
        if (!path.parent_path().empty()) {
            std::filesystem::create_directories(path.parent_path());
        }
        file.open(path);
        if (!file) {
            throw std::runtime_error("cannot open output file: " + options.output);
        }
        output = &file;
    }
    *output << std::fixed << std::setprecision(6);
    *output << "{\n"
            << "  \"schema_version\": 1,\n"
            << "  \"tool\": \"cpp-libcurl\",\n"
            << "  \"scenario\": \"" << json_escape(options.scenario) << "\",\n"
            << "  \"started_at\": \"" << started_at_utc(started_at) << "\",\n"
            << "  \"target\": \"" << json_escape(options.url) << "\",\n"
            << "  \"requests\": " << samples.size() << ",\n"
            << "  \"warmup_requests\": " << options.warmup << ",\n"
            << "  \"concurrency\": " << options.concurrency << ",\n"
            << "  \"gateway_instances\": " << options.instances << ",\n"
            << "  \"duration_ms\": " << duration_ms << ",\n"
            << "  \"throughput_rps\": " << throughput << ",\n"
            << "  \"latency_ms\": {\n"
            << "    \"min\": " << static_cast<double>(*std::min_element(latencies.begin(), latencies.end())) / 1000.0 << ",\n"
            << "    \"mean\": " << mean_ms << ",\n"
            << "    \"p50\": " << static_cast<double>(nearest_rank(latencies, 0.50)) / 1000.0 << ",\n"
            << "    \"p95\": " << static_cast<double>(nearest_rank(latencies, 0.95)) / 1000.0 << ",\n"
            << "    \"p99\": " << static_cast<double>(nearest_rank(latencies, 0.99)) / 1000.0 << ",\n"
            << "    \"max\": " << static_cast<double>(*std::max_element(latencies.begin(), latencies.end())) / 1000.0 << "\n"
            << "  },\n  \"status_counts\": ";
    write_string_map(*output, statuses, 4);
    *output << ",\n"
            << "  \"transport_errors\": " << transport_errors << ",\n"
            << "  \"http_errors\": " << http_errors << ",\n"
            << "  \"error_rate_percent\": " << error_rate << ",\n"
            << "  \"rate_limited\": " << rate_limited << ",\n"
            << "  \"upstream_counts\": ";
    write_string_map(*output, upstreams, 4);
    *output << ",\n"
            << "  \"verification\": {\n"
            << "    \"passed\": " << (passed ? "true" : "false") << ",\n"
            << "    \"checks\": {\n"
            << "      \"no_transport_errors\": " << (transport_errors == 0 ? "true" : "false") << ",\n"
            << "      \"status_codes_allowed\": " << (statuses_allowed ? "true" : "false") << ",\n"
            << "      \"minimum_upstreams_observed\": " << (upstreams_observed ? "true" : "false") << "\n"
            << "    },\n"
            << "    \"accepted\": " << statuses["200"] << ",\n"
            << "    \"denied\": " << statuses["429"] << ",\n"
            << "    \"observed_upstreams\": " << upstreams.size() << "\n"
            << "  },\n"
            << "  \"environment\": {\n"
            << "    \"compiler\": \"" << json_escape(__VERSION__) << "\",\n"
            << "    \"libcurl\": \"" << json_escape(curl_version()) << "\",\n"
            << "    \"os\": \"linux\",\n"
            << "    \"architecture\": \"" << architecture() << "\"\n"
            << "  },\n"
            << "  \"samples\": [\n";
    for (std::size_t index = 0; index < samples.size(); ++index) {
        const Sample& sample = samples[index];
        *output << "    {\"latency_us\": " << sample.latency_us
                << ", \"status_code\": " << sample.status_code
                << ", \"transport_error\": " << (sample.transport_error ? "true" : "false")
                << ", \"rate_limit_headers_valid\": " << (sample.rate_limit_headers_valid ? "true" : "false");
        if (!sample.upstream.empty()) {
            *output << ", \"upstream\": \"" << json_escape(sample.upstream) << "\"";
        }
        *output << "}" << (index + 1 == samples.size() ? "\n" : ",\n");
    }
    *output << "  ]\n}\n";
    return passed;
}

}  // namespace

int main(int argc, char** argv) {
    try {
        const Options options = parse_options(argc, argv);
        const char* raw_api_key = std::getenv("API_KEY");
        if (raw_api_key == nullptr || std::string(raw_api_key).empty()) {
            throw std::runtime_error("API_KEY environment variable is required");
        }
        if (curl_global_init(CURL_GLOBAL_DEFAULT) != CURLE_OK) {
            throw std::runtime_error("curl_global_init failed");
        }

        {
            CurlWorker warmup_worker(options, raw_api_key);
            for (int index = 0; index < options.warmup; ++index) {
                const Sample sample = warmup_worker.request();
                if (sample.transport_error) {
                    throw std::runtime_error("warmup request failed");
                }
            }
        }

        std::vector<Sample> samples(static_cast<std::size_t>(options.requests));
        std::atomic<int> next{0};
        std::mutex start_mutex;
        std::condition_variable start_condition;
        bool start = false;
        std::vector<std::thread> workers;
        workers.reserve(static_cast<std::size_t>(options.concurrency));
        for (int worker = 0; worker < options.concurrency; ++worker) {
            workers.emplace_back([&]() {
                CurlWorker client(options, raw_api_key);
                {
                    std::unique_lock lock(start_mutex);
                    start_condition.wait(lock, [&]() { return start; });
                }
                while (true) {
                    const int index = next.fetch_add(1);
                    if (index >= options.requests) {
                        return;
                    }
                    samples[static_cast<std::size_t>(index)] = client.request();
                }
            });
        }
        const auto started_at = std::chrono::system_clock::now();
        const auto wall_started = std::chrono::steady_clock::now();
        {
            std::lock_guard lock(start_mutex);
            start = true;
        }
        start_condition.notify_all();
        for (std::thread& worker : workers) {
            worker.join();
        }
        const auto duration = std::chrono::steady_clock::now() - wall_started;
        const bool passed = write_result(options, samples, started_at, duration);
        curl_global_cleanup();
        return passed ? 0 : 1;
    } catch (const std::exception& error) {
        std::cerr << "error: " << error.what() << '\n';
        curl_global_cleanup();
        return 2;
    }
}
