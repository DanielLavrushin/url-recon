import { useState, useCallback } from "react";
import {
  Container,
  Typography,
  CssBaseline,
  ThemeProvider,
  TextField,
  Button,
  Paper,
  Chip,
  Alert,
  Stack,
  Box,
} from "@mui/material";
import SearchIcon from "@mui/icons-material/Search";
import FileDownloadIcon from "@mui/icons-material/FileDownload";

import { theme } from "./theme";
import { useJobManager } from "./hooks/useJobManager";
import { ScanProgress } from "./components/ScanProgress";
import { ResultsTable } from "./components/ResultsTable";
import { ExportDialog } from "./components/ExportDialog";

function App() {
  const [url, setUrl] = useState("");
  const [exportOpen, setExportOpen] = useState(false);

  const { jobId, progress, result, error, isLoading, startScan } =
    useJobManager();

  const handleScan = useCallback(async () => {
    if (!url.trim()) return;
    await startScan(url.trim());
  }, [url, startScan]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !isLoading) {
      handleScan();
    }
  };

  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <Container maxWidth="xl" sx={{ py: 4 }}>
        <Typography variant="h3" component="h1" gutterBottom align="center">
          URL Recon
        </Typography>
        <Typography
          variant="body1"
          color="text.secondary"
          align="center"
          sx={{ mb: 4 }}
        >
          Discover domains referenced in any webpage
        </Typography>

        <Paper sx={{ p: 3, mb: 4 }}>
          <Stack direction={{ xs: "column", sm: "row" }} spacing={2}>
            <TextField
              fullWidth
              label="Enter URL to scan"
              placeholder="https://example.com"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onKeyDown={handleKeyDown}
              disabled={isLoading}
            />
            <Button
              variant="contained"
              onClick={handleScan}
              disabled={isLoading || !url.trim()}
              startIcon={<SearchIcon />}
              sx={{ minWidth: 120 }}
            >
              {isLoading ? "Scanning..." : "Scan"}
            </Button>
          </Stack>
          {jobId && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ mt: 1, display: "block" }}
            >
              Job ID: {jobId}
            </Typography>
          )}
        </Paper>

        {isLoading && progress && <ScanProgress progress={progress} />}

        {error && (
          <Alert severity="error" sx={{ mb: 4 }}>
            {error}
          </Alert>
        )}

        {result && (
          <>
            <Paper sx={{ p: 3, mb: 4 }}>
              <Box
                sx={{
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "flex-start",
                }}
              >
                <Box>
                  <Typography variant="h6" gutterBottom>
                    Results for {result.target_domain}
                  </Typography>
                  <Stack direction="row" spacing={2} flexWrap="wrap" useFlexGap>
                    <Chip
                      label={`Total: ${result.stats.total_domains}`}
                      color="primary"
                    />
                    <Chip
                      label={`External: ${result.stats.external_domains}`}
                      color="warning"
                    />
                    <Chip
                      label={`Internal: ${result.stats.internal_domains}`}
                      color="success"
                    />
                  </Stack>
                </Box>
                <Button
                  variant="outlined"
                  startIcon={<FileDownloadIcon />}
                  onClick={() => setExportOpen(true)}
                >
                  Export
                </Button>
              </Box>
            </Paper>

            <ResultsTable domains={result.domains} />

            <ExportDialog
              open={exportOpen}
              onClose={() => setExportOpen(false)}
              result={result}
            />
          </>
        )}
      </Container>
    </ThemeProvider>
  );
}

export default App;
