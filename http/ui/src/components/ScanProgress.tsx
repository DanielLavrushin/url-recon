import {
  Box,
  LinearProgress,
  Paper,
  Step,
  StepLabel,
  Stepper,
  Typography,
} from "@mui/material";
import type { Progress } from "../types";

const STAGES = [
  "Starting browser",
  "Loading page",
  "Extracting domains",
  "Resolving DNS",
  "Complete",
];

interface ScanProgressProps {
  progress: Progress;
}

export function ScanProgress({ progress }: Readonly<ScanProgressProps>) {
  const activeStep = STAGES.indexOf(progress.stage);
  const dnsProgress =
    progress.stage === "Resolving DNS" && progress.total > 0
      ? Math.round((progress.current / progress.total) * 100)
      : null;

  return (
    <Paper sx={{ p: 3, mb: 4 }}>
      <Stepper activeStep={activeStep} alternativeLabel>
        {STAGES.map((label) => (
          <Step key={label}>
            <StepLabel>{label}</StepLabel>
          </Step>
        ))}
      </Stepper>
      {dnsProgress !== null && (
        <Box sx={{ mt: 3 }}>
          <Box sx={{ display: "flex", justifyContent: "space-between", mb: 1 }}>
            <Typography variant="body2" color="text.secondary">
              Resolving domains...
            </Typography>
            <Typography variant="body2" color="text.secondary">
              {progress.current} / {progress.total}
            </Typography>
          </Box>
          <LinearProgress variant="determinate" value={dnsProgress} />
        </Box>
      )}
    </Paper>
  );
}
