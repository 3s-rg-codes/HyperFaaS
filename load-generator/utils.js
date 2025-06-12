// Function to generate a random integer between min and max
export function getRandomInt(min, max) {
  min = Math.ceil(min);
  max = Math.floor(max);
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

// Function to convert Unix nanosecond timestamp strings to nanoseconds
// Only supports nanosecond Unix timestamps from Go's UnixNano()
export function unixNanoToNs(timestampString) {
  // Parse the nanosecond timestamp string and return as number
  return parseInt(timestampString);
}

// Legacy alias for backward compatibility - but now preserves nanosecond precision
export function isoToMs(timestampString) {
  return unixNanoToNs(timestampString);
}

// Function to convert duration strings like "60s", "1m30s", "2h15m" to seconds
export function parseK6Duration(durationStr) {
  if (typeof durationStr !== 'string') return 0;
  let totalSeconds = 0;
  const parts = durationStr.match(/(\d+h)?(\d+m)?(\d+s)?/);
  if (!parts) return 0;
  if (parts[1]) totalSeconds += parseInt(parts[1].slice(0, -1)) * 3600;
  if (parts[2]) totalSeconds += parseInt(parts[2].slice(0, -1)) * 60;
  if (parts[3]) totalSeconds += parseInt(parts[3].slice(0, -1));
  return totalSeconds;
}